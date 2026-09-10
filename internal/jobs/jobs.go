// Package jobs выполняет длинные дела в фоне так, чтобы браузер был им не
// нужен.
//
// Задание живёт в базе (store.Job): его шаги, журнал и исход пишутся по
// ходу. Поэтому закрытая вкладка, обрыв сети, другой компьютер и возврат
// через час — это просто чтение той же записи, а не потеря хода работы.
// Перезапуск самой службы задание переживает иначе: горутина умирает
// вместе с процессом, но при следующем запуске Recover разбирает
// незавершённые — те, чей исполнитель умеет продолжать, запускаются
// заново с сохранённого места, остальные честно помечаются прерванными.
//
// Очередь — по ключу (обычно это хост или ресурс): на одном ключе
// одновременно выполняется одно задание. Две установки пакетов или две
// правки одного конфига разом кончаются беспорядком, который потом никто
// не разберёт.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/piqab/nkt/internal/store"
)

// Runner выполняет задания одного вида.
type Runner interface {
	// Run выполняет задание. Возврат ошибки — провал; возврат nil —
	// успех. Отмена и остановка службы приходят через ctx.
	Run(ctx context.Context, jc *Context) error
}

// Resumable реализует исполнитель, который умеет продолжать прерванное
// задание с сохранённого места. Всё, что этого не умеет, после
// перезапуска службы остаётся прерванным — молча начинать заново то, что
// уже наполовину применено к чужому серверу, нельзя.
type Resumable interface {
	Resumable() bool
}

// Context — то, что исполнитель получает на руки: сама запись задания,
// журнал и способ отметить шаг.
type Context struct {
	Job store.Job

	m *Manager
}

// Logf дописывает строку в журнал задания — она сразу уходит и в базу, и
// подключённым наблюдателям.
func (jc *Context) Logf(format string, args ...any) {
	jc.m.appendLog(jc.Job.ID, fmt.Sprintf(format, args...))
}

// Step отмечает переход к следующему шагу: n из total.
func (jc *Context) Step(n, total int, name string) {
	jc.m.setStep(jc.Job.ID, n, total, name)
}

// Params разбирает вход задания.
func (jc *Context) Params(v any) error {
	if jc.Job.Params == "" {
		return nil
	}
	return json.Unmarshal([]byte(jc.Job.Params), v)
}

// LoadResume читает сохранённое состояние продолжения.
func (jc *Context) LoadResume(v any) error {
	if jc.Job.Resume == "" {
		return nil
	}
	return json.Unmarshal([]byte(jc.Job.Resume), v)
}

// SaveResume запоминает, докуда дошли. Вызывать после каждого шага,
// который не нужно повторять: именно отсюда задание продолжится после
// перезапуска службы.
func (jc *Context) SaveResume(v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	jc.Job.Resume = string(raw)
	jc.m.setResume(jc.Job.ID, string(raw))
}

// Update — событие для подключённых наблюдателей.
type Update struct {
	// Line — новая строка журнала (пустая, если это смена статуса).
	Line *store.JobLogLine `json:"line,omitempty"`
	// Job — состояние задания на момент события.
	Job *store.Job `json:"job,omitempty"`
}

// Manager хранит очереди и подписки.
type Manager struct {
	db  *store.DB
	log *slog.Logger

	mu      sync.Mutex
	runners map[string]Runner
	// running — идущие задания по ключу очереди; значение — отмена.
	running map[string]context.CancelFunc
	// pending — что ждёт своей очереди на ключе.
	pending map[string][]int64
	// watchers — подписчики по идентификатору задания.
	watchers map[int64][]chan Update
	// userCanceled — задания, отменённые оператором. Нужно, чтобы
	// отличить отмену от остановки службы: ctx в обоих случаях
	// одинаковый, а исход должен быть разным.
	userCanceled map[int64]bool
	// closed — менеджер остановлен вместе со службой.
	closed bool
}

// New строит менеджер.
func New(db *store.DB, log *slog.Logger) *Manager {
	return &Manager{
		db:           db,
		log:          log,
		runners:      map[string]Runner{},
		running:      map[string]context.CancelFunc{},
		pending:      map[string][]int64{},
		watchers:     map[int64][]chan Update{},
		userCanceled: map[int64]bool{},
	}
}

// Register привязывает исполнителя к виду задания.
func (m *Manager) Register(kind string, r Runner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runners[kind] = r
}

// Spec — что запускаем.
type Spec struct {
	Kind   string
	Title  string
	Queue  string
	Author string
	Params any
	Steps  int
}

// Start заводит задание и запускает его, если ключ очереди свободен.
func (m *Manager) Start(ctx context.Context, spec Spec) (int64, error) {
	m.mu.Lock()
	_, known := m.runners[spec.Kind]
	m.mu.Unlock()
	if !known {
		return 0, fmt.Errorf("неизвестный вид задания %q", spec.Kind)
	}

	params := ""
	if spec.Params != nil {
		raw, err := json.Marshal(spec.Params)
		if err != nil {
			return 0, err
		}
		params = string(raw)
	}
	id, err := m.db.CreateJob(ctx, store.Job{
		Kind: spec.Kind, Title: spec.Title, Queue: spec.Queue, Status: store.JobQueued,
		Params: params, Steps: spec.Steps, Author: spec.Author,
	})
	if err != nil {
		return 0, err
	}
	m.enqueue(id, spec.Queue)
	return id, nil
}

// enqueue ставит задание в очередь ключа и запускает, если ключ свободен.
func (m *Manager) enqueue(id int64, queue string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if _, busy := m.running[queue]; busy {
		m.pending[queue] = append(m.pending[queue], id)
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.running[queue] = cancel
	m.mu.Unlock()

	go m.run(ctx, id, queue)
}

// run выполняет одно задание и передаёт очередь следующему.
func (m *Manager) run(ctx context.Context, id int64, queue string) {
	defer m.next(queue)

	job, err := m.db.JobByID(context.Background(), id)
	if err != nil {
		m.log.Error("задание не прочитано", "id", id, "err", err)
		return
	}
	m.mu.Lock()
	runner := m.runners[job.Kind]
	m.mu.Unlock()
	if runner == nil {
		_ = m.db.FinishJob(context.Background(), id, store.JobFailed,
			fmt.Sprintf("неизвестный вид задания %q", job.Kind))
		m.notifyJob(id)
		return
	}

	if err := m.db.MarkJobRunning(context.Background(), id); err != nil {
		m.log.Error("задание не отмечено идущим", "id", id, "err", err)
	}
	m.notifyJob(id)

	job, _ = m.db.JobByID(context.Background(), id)
	jc := &Context{Job: job, m: m}
	runErr := runner.Run(ctx, jc)

	m.mu.Lock()
	byUser := m.userCanceled[id]
	delete(m.userCanceled, id)
	stopping := m.closed
	m.mu.Unlock()

	// Служба останавливается — исход не записываем вовсе. Задание
	// остаётся в базе идущим, и следующий запуск разберёт его в Recover:
	// продолжит, если исполнитель это умеет, иначе пометит прерванным.
	// Записать здесь «отменено» значило бы навсегда потерять возможность
	// продолжить — и соврать, потому что никто ничего не отменял.
	if stopping {
		m.appendLog(id, "Служба останавливается — задание прервано на середине.")
		m.closeWatchers(id)
		return
	}

	status, msg := store.JobSucceeded, ""
	switch {
	case byUser:
		status, msg = store.JobCanceled, "отменено оператором"
	case runErr != nil:
		status, msg = store.JobFailed, runErr.Error()
	}
	if runErr != nil {
		m.appendLog(id, "— "+runErr.Error())
	}
	if err := m.db.FinishJob(context.Background(), id, status, msg); err != nil {
		m.log.Error("исход задания не записан", "id", id, "err", err)
	}
	m.notifyJob(id)
	m.closeWatchers(id)
}

// next запускает следующее задание того же ключа очереди.
func (m *Manager) next(queue string) {
	m.mu.Lock()
	delete(m.running, queue)
	list := m.pending[queue]
	if len(list) == 0 || m.closed {
		delete(m.pending, queue)
		m.mu.Unlock()
		return
	}
	id := list[0]
	m.pending[queue] = list[1:]
	ctx, cancel := context.WithCancel(context.Background())
	m.running[queue] = cancel
	m.mu.Unlock()

	go m.run(ctx, id, queue)
}

// Cancel останавливает идущее задание или убирает ждущее из очереди.
func (m *Manager) Cancel(ctx context.Context, id int64) error {
	job, err := m.db.JobByID(ctx, id)
	if err != nil {
		return err
	}
	if job.Done() {
		return fmt.Errorf("задание уже завершено")
	}

	m.mu.Lock()
	// Ждущее в очереди отменяется прямо здесь: его горутина ещё не
	// запускалась, и отменять нечего.
	list := m.pending[job.Queue]
	for i, pid := range list {
		if pid == id {
			m.pending[job.Queue] = append(list[:i:i], list[i+1:]...)
			m.mu.Unlock()
			_ = m.db.FinishJob(ctx, id, store.JobCanceled, "отменено до запуска")
			m.notifyJob(id)
			m.closeWatchers(id)
			return nil
		}
	}
	cancel := m.running[job.Queue]
	if cancel != nil {
		m.userCanceled[id] = true
	}
	m.mu.Unlock()

	if cancel == nil {
		return fmt.Errorf("задание не выполняется в этом процессе")
	}
	cancel()
	return nil
}

// Recover разбирает задания, оставшиеся идущими от прошлого запуска
// службы. Вызывается один раз при старте, до приёма запросов.
func (m *Manager) Recover(ctx context.Context) error {
	jobs, err := m.db.UnfinishedJobs(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		m.mu.Lock()
		runner := m.runners[job.Kind]
		m.mu.Unlock()

		resumable := false
		if r, ok := runner.(Resumable); ok {
			resumable = r.Resumable()
		}
		if runner == nil || !resumable {
			m.appendLog(job.ID, "Служба перезапустилась — задание прервано.")
			_ = m.db.FinishJob(ctx, job.ID, store.JobInterrupted, "прервано перезапуском службы")
			continue
		}
		m.appendLog(job.ID, "Служба перезапустилась — продолжаю с сохранённого места.")
		m.enqueue(job.ID, job.Queue)
	}
	return nil
}

// Close останавливает идущие задания при остановке службы. Их статус в
// базе остаётся «идёт»: следующий запуск увидит это в Recover и решит,
// продолжать или пометить прерванным. Дописывать исход здесь нельзя — при
// падении процесса эта строка и не выполнится, а разбор всё равно нужен.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	cancels := make([]context.CancelFunc, 0, len(m.running))
	for _, c := range m.running {
		cancels = append(cancels, c)
	}
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

// Watch подписывает на события задания. Возвращает канал и функцию
// отписки; канал закрывается, когда задание завершилось.
func (m *Manager) Watch(id int64) (<-chan Update, func()) {
	ch := make(chan Update, 64)
	m.mu.Lock()
	m.watchers[id] = append(m.watchers[id], ch)
	m.mu.Unlock()

	return ch, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		list := m.watchers[id]
		for i, c := range list {
			if c == ch {
				m.watchers[id] = append(list[:i:i], list[i+1:]...)
				close(c)
				break
			}
		}
	}
}

// appendLog пишет строку в базу и рассылает наблюдателям.
func (m *Manager) appendLog(id int64, text string) {
	if err := m.db.AppendJobLog(context.Background(), id, text); err != nil {
		m.log.Error("строка журнала задания не записана", "id", id, "err", err)
		return
	}
	m.notify(id, Update{Line: &store.JobLogLine{Text: text}})
}

func (m *Manager) setStep(id int64, n, total int, name string) {
	if err := m.db.SetJobStep(context.Background(), id, n, total, name); err != nil {
		m.log.Error("шаг задания не записан", "id", id, "err", err)
	}
	m.notifyJob(id)
}

func (m *Manager) setResume(id int64, resume string) {
	if err := m.db.SetJobResume(context.Background(), id, resume); err != nil {
		m.log.Error("состояние продолжения не записано", "id", id, "err", err)
	}
}

func (m *Manager) notifyJob(id int64) {
	job, err := m.db.JobByID(context.Background(), id)
	if err != nil {
		return
	}
	m.notify(id, Update{Job: &job})
}

// notify рассылает событие. Наблюдатель, который не успевает читать,
// пропускает событие, а не тормозит само задание: полный журнал он всегда
// может дочитать из базы.
//
// Отправка идёт под тем же замком, что закрытие каналов: иначе отправка в
// канал, который в этот момент закрывает closeWatchers, роняет процесс.
// Блокировки это не создаёт — все отправки неблокирующие.
func (m *Manager) notify(id int64, u Update) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.watchers[id] {
		select {
		case ch <- u:
		default:
		}
	}
}

func (m *Manager) closeWatchers(id int64) {
	m.mu.Lock()
	list := m.watchers[id]
	delete(m.watchers, id)
	m.mu.Unlock()
	for _, ch := range list {
		close(ch)
	}
}
