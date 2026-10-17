package hub

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"
)

// oneTimeSecrets — секреты на один раз (пароль sudo для задания): только
// в памяти хаба, не в параметрах задания, не в базе и не в журнале. В
// параметры кладётся жетон; задание забирает секрет по нему и тем самым
// стирает. Неиспользованный секрет пропадает через oneTimeTTL, а
// перезапуск хаба теряет их все — задание тогда просит ввести заново.
type oneTimeSecrets struct {
	mu sync.Mutex
	m  map[string]oneTimeItem
}

type oneTimeItem struct {
	val []byte
	exp time.Time
}

const oneTimeTTL = 15 * time.Minute

var oneTime = &oneTimeSecrets{m: map[string]oneTimeItem{}}

// Put — сохранить секрет; ответ — жетон.
func (o *oneTimeSecrets) Put(val []byte) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	tok := hex.EncodeToString(b[:])
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sweep(time.Now())
	o.m[tok] = oneTimeItem{val: append([]byte(nil), val...), exp: time.Now().Add(oneTimeTTL)}
	return tok
}

// Take — забрать секрет (второй раз его уже нет). Вызывающий стирает
// полученные байты, когда они больше не нужны.
func (o *oneTimeSecrets) Take(tok string) ([]byte, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sweep(time.Now())
	it, ok := o.m[tok]
	if !ok {
		return nil, false
	}
	delete(o.m, tok)
	return it.val, true
}

func (o *oneTimeSecrets) sweep(now time.Time) {
	for k, it := range o.m {
		if now.After(it.exp) {
			wipe(it.val)
			delete(o.m, k)
		}
	}
}

// wipe — затереть секрет в памяти.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Set — секрет под известным ключом (например, пароль SSH хоста до
// перехода на ключ): его читают несколько попыток установки, а стирает
// Delete после успеха или срок ttl.
func (o *oneTimeSecrets) Set(key string, val []byte, ttl time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sweep(time.Now())
	if old, ok := o.m[key]; ok {
		wipe(old.val)
	}
	o.m[key] = oneTimeItem{val: append([]byte(nil), val...), exp: time.Now().Add(ttl)}
}

// Get — копия секрета по ключу, не стирая его.
func (o *oneTimeSecrets) Get(key string) ([]byte, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sweep(time.Now())
	it, ok := o.m[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), it.val...), true
}

// Delete — стереть секрет.
func (o *oneTimeSecrets) Delete(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if it, ok := o.m[key]; ok {
		wipe(it.val)
		delete(o.m, key)
	}
}

// Ключи секретов хоста в oneTime и сколько они живут: пароль SSH — до
// перехода хоста на ключ, пароль sudo и пароль нового пользователя — до
// конца установки.
const hostSecretTTL = time.Hour

func sshPasswordKey(hostID int64) string  { return "ssh:" + strconv.FormatInt(hostID, 10) }
func sudoPasswordKey(hostID int64) string { return "sudo:" + strconv.FormatInt(hostID, 10) }
func userPasswordKey(hostID int64) string { return "userpw:" + strconv.FormatInt(hostID, 10) }
