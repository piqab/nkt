package hub

import (
	"crypto/rand"
	"encoding/hex"
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
