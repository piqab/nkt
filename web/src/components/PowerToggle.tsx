import { RowAction } from './RowAction'

/**
 * Одна кнопка вместо пары «запустить / остановить»: по состоянию строки
 * показывается ровно то действие, которое сейчас имеет смысл. Работает —
 * красная «остановить» (с подтверждением у вызывающего); остановлен —
 * синяя «запустить»; на паузе — «возобновить»; в переходном состоянии
 * (запускается, перезапускается, останавливается) — занятая кнопка без
 * действия, чтобы второй клик не ушёл в середину перехода.
 */
export type PowerState = 'running' | 'stopped' | 'paused' | 'transitional'

export function PowerToggle({
  state,
  labels,
  loading,
  disabled,
  onStart,
  onStop,
  onResume,
}: {
  state: PowerState
  labels: { start: string; stop: string; resume?: string; busy?: string }
  loading?: boolean
  disabled?: boolean
  onStart: () => void
  onStop: () => void
  onResume?: () => void
}) {
  if (state === 'transitional') {
    return <RowAction action="reload" label={labels.busy ?? labels.stop} loading disabled onClick={() => undefined} />
  }
  if (state === 'running') {
    return <RowAction action="stop" label={labels.stop} danger loading={loading} disabled={disabled} onClick={onStop} />
  }
  if (state === 'paused' && onResume) {
    return <RowAction action="resume" label={labels.resume ?? labels.start} loading={loading} disabled={disabled} onClick={onResume} />
  }
  return <RowAction action="start" label={labels.start} loading={loading} disabled={disabled} onClick={onStart} />
}

/** Состояние контейнера Docker/Podman/LXD по строке состояния движка. */
export function containerPowerState(state: string): PowerState {
  const s = state.toLowerCase()
  if (s === 'running') return 'running'
  if (s === 'paused' || s === 'frozen') return 'paused'
  if (s === 'restarting' || s === 'removing' || s === 'starting' || s === 'stopping') return 'transitional'
  return 'stopped'
}

/** Состояние службы systemd по ActiveState. */
export function servicePowerState(activeState: string): PowerState {
  switch (activeState) {
    case 'active':
      return 'running'
    case 'activating':
    case 'deactivating':
    case 'reloading':
      return 'transitional'
    default:
      return 'stopped'
  }
}

/** Состояние машины libvirt по `virsh domstate`. */
export function vmPowerState(state: string): PowerState {
  const s = state.toLowerCase()
  if (s === 'running' || s === 'idle' || s === 'blocked') return 'running'
  if (s === 'paused' || s === 'pmsuspended') return 'paused'
  if (s === 'in shutdown') return 'transitional'
  return 'stopped'
}
