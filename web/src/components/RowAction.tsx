import type { ReactNode } from 'react'
import { Button, Tooltip } from 'antd'
import {
  ApiOutlined,
  CaretRightOutlined,
  CheckCircleOutlined,
  CloudDownloadOutlined,
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  ExportOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  HistoryOutlined,
  KeyOutlined,
  LinkOutlined,
  PauseOutlined,
  PlusOutlined,
  PoweroffOutlined,
  ReloadOutlined,
  SaveOutlined,
  StopOutlined,
  SyncOutlined,
  ThunderboltOutlined,
  UploadOutlined,
} from '@ant-design/icons'

/**
 * Действие над строкой таблицы — иконкой, с названием по наведению.
 *
 * Словами каждое действие занимало место, которого в строке нет: у
 * контейнера их пять, у службы до семи, и строка либо расползалась
 * вширь, либо разваливалась на несколько строк. Иконка занимает один
 * символ, а название никуда не девается — оно в подсказке и в
 * aria-label, то есть и для мыши, и для чтения с экрана, и на том языке,
 * который выбран в интерфейсе.
 *
 * Словарь иконок общий на всё приложение: «запустить» выглядит одинаково
 * у службы, контейнера и машины — иначе иконки пришлось бы разгадывать
 * каждый раз заново.
 */
export const ACTION_ICON: Record<string, ReactNode> = {
  start: <CaretRightOutlined />,
  stop: <PoweroffOutlined />,
  shutdown: <PoweroffOutlined />,
  destroy: <StopOutlined />,
  'force-off': <StopOutlined />,
  restart: <ReloadOutlined />,
  reboot: <ReloadOutlined />,
  reload: <SyncOutlined />,
  resume: <CaretRightOutlined />,
  suspend: <PauseOutlined />,
  enable: <ThunderboltOutlined />,
  disable: <StopOutlined />,
  autostart: <ThunderboltOutlined />,
  validate: <CheckCircleOutlined />,
  edit: <EditOutlined />,
  delete: <DeleteOutlined />,
  logs: <FileTextOutlined />,
  log: <FileTextOutlined />,
  history: <HistoryOutlined />,
  open: <ExportOutlined />,
  link: <LinkOutlined />,
  install: <DownloadOutlined />,
  update: <CloudDownloadOutlined />,
  download: <DownloadOutlined />,
  upload: <UploadOutlined />,
  save: <SaveOutlined />,
  key: <KeyOutlined />,
  add: <PlusOutlined />,
  create: <PlusOutlined />,
  details: <FileSearchOutlined />,
  address: <ApiOutlined />,
  kill: <StopOutlined />,
}

export function RowAction({
  action,
  label,
  icon,
  danger,
  loading,
  disabled,
  onClick,
}: {
  /** Имя действия из общего словаря — им и выбирается иконка. */
  action?: string
  /** Название на языке интерфейса: подсказка при наведении и для чтения
   * с экрана. */
  label: string
  /** Своя иконка, когда действие не из словаря. */
  icon?: ReactNode
  danger?: boolean
  loading?: boolean
  disabled?: boolean
  onClick: () => void
}) {
  const glyph = icon ?? (action ? ACTION_ICON[action] : undefined)
  // Неизвестному действию иконку не выдумываем: пусть лучше останется
  // словом, чем превратится в загадку.
  if (!glyph) {
    return (
      <Button type="link" size="small" danger={danger} loading={loading} disabled={disabled} onClick={onClick}>
        {label}
      </Button>
    )
  }
  return (
    <Tooltip title={label}>
      <Button
        type="text"
        size="small"
        aria-label={label}
        icon={glyph}
        danger={danger}
        loading={loading}
        disabled={disabled}
        onClick={onClick}
      />
    </Tooltip>
  )
}
