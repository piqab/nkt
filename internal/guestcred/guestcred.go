// Package guestcred хранит логин и пароль гостей — инстансов LXD и машин
// libvirt, — заданные через nkt. Пароль шифруется (AES-256-GCM, ключ в
// каталоге данных хоста) и показывается только администратору по кнопке,
// с записью в аудит. Нигде больше он не лежит: ни в параметрах заданий, ни
// в командной строке — задание берёт его отсюда по ссылке.
package guestcred

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Info — что показывается без пароля.
type Info struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	User  string `json:"user,omitempty"`
	Set   bool   `json:"set"`
	SetAt string `json:"set_at,omitempty"`
	SetBy string `json:"set_by,omitempty"`
}

type record struct {
	User    string `json:"user"`
	PassEnc string `json:"pass_enc"`
	SetAt   string `json:"set_at"`
	SetBy   string `json:"set_by"`
}

// Store — хранилище.
type Store struct {
	db      *store.DB
	keyPath string
	mu      sync.Mutex
	key     []byte
}

// New — ключ создаётся при первом сохранении пароля.
func New(db *store.DB, dataDir string) *Store {
	return &Store{db: db, keyPath: filepath.Join(dataDir, "guest-secrets.key")}
}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	userRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// ValidTarget — вид (lxd | vm) и имя гостя.
func ValidTarget(kind, name string) bool {
	return (kind == "lxd" || kind == "vm") && nameRe.MatchString(name)
}

// ValidUser — имя учётной записи Linux.
func ValidUser(user string) bool { return userRe.MatchString(user) }

// ValidPassword — 8–72 байта без перевода строки (chpasswd читает
// построчно, «:» внутри пароля он понимает; 72 байта — предел bcrypt, им
// хэшируется пароль машины libvirt в cloud-init).
func ValidPassword(p string) bool {
	return len(p) >= 8 && len(p) <= 72 && !strings.ContainsAny(p, "\r\n\x00")
}

func kvKey(kind, name string) string { return "guestcred:" + kind + ":" + name }

// Ref — ссылка на пароль для задания «выполнить команды».
func Ref(kind, name string) string { return kind + ":" + name }

func (s *Store) cipherKey() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != nil {
		return s.key, nil
	}
	k, err := secretbox.ResolveKey("", s.keyPath)
	if err != nil {
		return nil, err
	}
	s.key = k
	return k, nil
}

// Put сохраняет логин и пароль.
func (s *Store) Put(ctx context.Context, kind, name, user, password, by string) error {
	if !ValidTarget(kind, name) || !ValidUser(user) || !ValidPassword(password) {
		return msgs.Errorf("guestcred.invalid")
	}
	key, err := s.cipherKey()
	if err != nil {
		return err
	}
	enc, err := secretbox.Encrypt(key, []byte(password))
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(record{User: user, PassEnc: base64.StdEncoding.EncodeToString(enc), SetAt: store.Now(), SetBy: by})
	return s.db.KVSet(ctx, kvKey(kind, name), string(raw))
}

func (s *Store) load(ctx context.Context, kind, name string) (record, bool, error) {
	var rec record
	if !ValidTarget(kind, name) {
		return rec, false, msgs.Errorf("guestcred.invalid")
	}
	raw, ok, err := s.db.KVGet(ctx, kvKey(kind, name))
	if err != nil || !ok || raw == "" {
		return rec, false, err
	}
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return rec, false, err
	}
	return rec, true, nil
}

// Info — логин и когда задан пароль, без самого пароля.
func (s *Store) Info(ctx context.Context, kind, name string) (Info, error) {
	info := Info{Kind: kind, Name: name}
	rec, ok, err := s.load(ctx, kind, name)
	if err != nil || !ok {
		return info, err
	}
	info.User, info.Set, info.SetAt, info.SetBy = rec.User, true, rec.SetAt, rec.SetBy
	return info, nil
}

// Reveal — логин и расшифрованный пароль.
func (s *Store) Reveal(ctx context.Context, kind, name string) (string, string, error) {
	rec, ok, err := s.load(ctx, kind, name)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", msgs.Errorf("guestcred.notSet", name)
	}
	enc, err := base64.StdEncoding.DecodeString(rec.PassEnc)
	if err != nil {
		return "", "", err
	}
	key, err := s.cipherKey()
	if err != nil {
		return "", "", err
	}
	plain, err := secretbox.Decrypt(key, enc)
	if err != nil {
		return "", "", err
	}
	return rec.User, string(plain), nil
}

// Secret — пароль по ссылке «вид:имя» (для заданий).
func (s *Store) Secret(ctx context.Context, ref string) (string, error) {
	kind, name, ok := strings.Cut(ref, ":")
	if !ok {
		return "", msgs.Errorf("guestcred.invalid")
	}
	_, pass, err := s.Reveal(ctx, kind, name)
	return pass, err
}

// Delete забывает пароль (гость удалён).
func (s *Store) Delete(ctx context.Context, kind, name string) error {
	if !ValidTarget(kind, name) {
		return nil
	}
	return s.db.KVSet(ctx, kvKey(kind, name), "")
}

// Generate — случайный пароль из 16 знаков без похожих (0/O, 1/l/I).
func Generate() string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var b strings.Builder
	for i := 0; i < 16; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		b.WriteByte(alphabet[n.Int64()])
	}
	return b.String()
}

// ShellQuote — строка в одинарных кавычках для sh.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
