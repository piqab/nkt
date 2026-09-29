package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"

	"github.com/piqab/nkt/internal/msgs"
)

// Разделы файла экспорта версии 4: конвейеры выкладок, учётные записи
// веб-интерфейса, шаблоны fail2ban, nkt-edge.

// PipelineExport — конвейер с секретами (зашифрованы мастер-ключом хаба,
// при импорте с ключом перешифровываются) и историей редакций. HookID
// переносится: вебхуки в GitHub/GitLab продолжают работать на новом хабе,
// если адрес там свободен.
type PipelineExport struct {
	Name         string                 `json:"name"`
	Content      string                 `json:"content"`
	HookID       string                 `json:"hook_id"`
	HookSecret   []byte                 `json:"hook_secret,omitempty"`
	GitCred      []byte                 `json:"git_cred,omitempty"`
	RegistryCred []byte                 `json:"registry_cred,omitempty"`
	EnvEnc       []byte                 `json:"env_enc,omitempty"`
	Enabled      bool                   `json:"enabled"`
	Author       string                 `json:"author,omitempty"`
	CreatedAt    string                 `json:"created_at,omitempty"`
	Versions     []ProfileVersionExport `json:"versions,omitempty"`
}

// UserExport — учётная запись веб-интерфейса с хэшем пароля (argon2id —
// сам пароль нигде не хранится).
type UserExport struct {
	Username     string `json:"username"`
	Role         string `json:"role"`
	Disabled     bool   `json:"disabled,omitempty"`
	PasswordHash string `json:"password_hash"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// F2BTemplateExport — свой шаблон fail2ban с историей (текст версии —
// тот же документ, что в общей истории: описание, джейл, фильтр).
type F2BTemplateExport struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Jail        string                 `json:"jail"`
	Filter      string                 `json:"filter,omitempty"`
	Versions    []ProfileVersionExport `json:"versions,omitempty"`
}

// EdgeExport — соединение с nkt-edge; Host — имя хоста, на который edge
// поставлен (идентификаторы в другом хабе другие).
type EdgeExport struct {
	Enabled     bool   `json:"enabled"`
	Address     string `json:"address,omitempty"`
	Domain      string `json:"domain,omitempty"`
	TokenEnc    []byte `json:"token_enc,omitempty"`
	CertPEM     string `json:"cert_pem,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Host        string `json:"host,omitempty"`
}

// ExportPipelines — конвейеры с секретами и историей (старые редакции
// первыми).
func (db *DB) ExportPipelines(ctx context.Context) ([]PipelineExport, error) {
	list, err := db.ListPipelines(ctx)
	if err != nil {
		return nil, err
	}
	var out []PipelineExport
	for _, p := range list {
		pe := PipelineExport{Name: p.Name, Content: p.Content, HookID: p.HookID, HookSecret: p.HookSecret,
			GitCred: p.GitCred, RegistryCred: p.RegistryCred, EnvEnc: p.EnvEnc, Enabled: p.Enabled, Author: p.Author, CreatedAt: p.CreatedAt}
		versions, err := db.PipelineVersions(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		for i := len(versions) - 1; i >= 0; i-- {
			v, err := db.PipelineVersion(ctx, versions[i].ID)
			if err != nil {
				return nil, err
			}
			pe.Versions = append(pe.Versions, ProfileVersionExport{TS: v.TS, Author: v.Author, Note: v.Note, Content: v.Content})
		}
		out = append(out, pe)
	}
	return out, nil
}

// ExportUsers — учётные записи веб-интерфейса.
func (db *DB) ExportUsers(ctx context.Context) ([]UserExport, error) {
	list, err := db.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserExport, 0, len(list))
	for _, u := range list {
		out = append(out, UserExport{Username: u.Username, Role: u.Role, Disabled: u.Disabled, PasswordHash: u.PasswordHash, CreatedAt: u.CreatedAt})
	}
	return out, nil
}

var hookIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func (db *DB) importPipelines(ctx context.Context, pipelines []PipelineExport, res ImportResolutions, rep *ImportReport) {
	existing, err := db.ListPipelines(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return
	}
	byName := map[string]Pipeline{}
	hooks := map[string]int64{}
	for _, p := range existing {
		byName[p.Name] = p
		hooks[p.HookID] = p.ID
	}
	for _, p := range pipelines {
		if p.Name == "" {
			continue
		}
		cnt := rep.Count(SectionPipelines)
		if !hookIDRe.MatchString(p.HookID) {
			rep.Err("%s: hook %q", p.Name, p.HookID)
			continue
		}
		var id int64
		if old, ok := byName[p.Name]; ok {
			if !res.Replace(SectionPipelines, p.Name) {
				cnt.Skipped++
				continue
			}
			id = old.ID
			if err := db.UpdatePipelineContent(ctx, id, p.Content, p.Author, importNote(ctx)); err != nil {
				rep.Err("%s: %v", p.Name, err)
				continue
			}
			// Адрес вебхука — из файла, если он свободен: иначе у
			// заменённого остаётся свой.
			if owner, taken := hooks[p.HookID]; !taken || owner == id {
				if _, err := db.ExecContext(ctx, `UPDATE pipelines SET hook_id = ? WHERE id = ?`, p.HookID, id); err == nil {
					delete(hooks, old.HookID)
					hooks[p.HookID] = id
				}
			} else {
				rep.Errors = append(rep.Errors, msgs.Tc(ctx, "store.importHookTaken", p.Name, p.HookID))
			}
			cnt.Replaced++
		} else {
			hook := p.HookID
			if _, taken := hooks[hook]; taken {
				// Адрес занят другим конвейером этого хаба — новый адрес,
				// вебхук в репозитории придётся поправить.
				rep.Errors = append(rep.Errors, msgs.Tc(ctx, "store.importHookTaken", p.Name, p.HookID))
				hook = p.HookID + "-" + randomSuffix()
			}
			id, err = db.CreatePipeline(ctx, Pipeline{Name: p.Name, Content: p.Content, HookID: hook, HookSecret: p.HookSecret, Author: p.Author})
			if err != nil {
				rep.Err("%s: %v", p.Name, err)
				continue
			}
			hooks[hook] = id
			if len(p.Versions) > 0 {
				if _, err := db.ExecContext(ctx, `DELETE FROM pipeline_versions WHERE pipeline_id = ?`, id); err != nil {
					rep.Err("%s: %v", p.Name, err)
				}
				for _, v := range p.Versions {
					if _, err := db.ExecContext(ctx, `INSERT INTO pipeline_versions (pipeline_id, ts, author, note, content) VALUES (?, ?, ?, ?, ?)`,
						id, v.TS, v.Author, v.Note, v.Content); err != nil {
						rep.Err("%s: %v", p.Name, err)
						break
					}
				}
			}
			cnt.Added++
		}
		empty := func(b []byte) []byte {
			if b == nil {
				return []byte{}
			}
			return b
		}
		if err := db.SetPipelineSecrets(ctx, id, empty(p.HookSecret), empty(p.GitCred), empty(p.RegistryCred)); err != nil {
			rep.Err("%s: %v", p.Name, err)
		}
		if len(p.EnvEnc) > 0 {
			if err := db.SetPipelineEnv(ctx, id, p.EnvEnc); err != nil {
				rep.Err("%s: %v", p.Name, err)
			}
		}
		if err := db.SetPipelineEnabled(ctx, id, p.Enabled); err != nil {
			rep.Err("%s: %v", p.Name, err)
		}
	}
}

func (db *DB) importUsers(ctx context.Context, users []UserExport, res ImportResolutions, rep *ImportReport) {
	for _, u := range users {
		if u.Username == "" || u.PasswordHash == "" {
			continue
		}
		cnt := rep.Count(SectionUsers)
		if u.Role != RoleAdmin && u.Role != RoleViewer {
			rep.Err("%s: role %q", u.Username, u.Role)
			continue
		}
		if _, err := db.UserByName(ctx, u.Username); err == nil {
			if !res.Replace(SectionUsers, u.Username) {
				cnt.Skipped++
				continue
			}
			if err := db.SetPasswordHash(ctx, u.Username, u.PasswordHash); err != nil {
				rep.Err("%s: %v", u.Username, err)
				continue
			}
			_ = db.SetUserRole(ctx, u.Username, u.Role)
			_ = db.SetUserDisabled(ctx, u.Username, u.Disabled)
			cnt.Replaced++
			continue
		}
		if _, err := db.CreateUser(ctx, u.Username, u.PasswordHash, u.Role); err != nil {
			rep.Err("%s: %v", u.Username, err)
			continue
		}
		if u.Disabled {
			_ = db.SetUserDisabled(ctx, u.Username, true)
		}
		cnt.Added++
	}
}

// randomSuffix — хвост для адреса вебхука, занятого в этом хабе.
func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
