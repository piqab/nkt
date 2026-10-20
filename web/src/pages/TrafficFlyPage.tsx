import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { fetchHubJob } from '../api'
import { TrafficFly } from '../components/TrafficFly'
import { TitleHelp } from '../components/Docs'
import type { Job, Me } from '../types'
import { JobLogModal } from './Jobs'

/** Traffic Fly — отдельным разделом хаба (раньше — вкладка «Выкладок»):
 * карта, маршруты, шлюзы, история и применение. */
export default function TrafficFlyPage({ me }: { me: Me }) {
  const { t } = useTranslation()
  const [job, setJob] = useState<Job | null>(null)
  return (
    <>
      <div className="page-head spread">
        <h1>
          Traffic Fly
          <TitleHelp>{t('fly.pageHint')}</TitleHelp>
        </h1>
      </div>
      <TrafficFly me={me} onOpenJob={(id) => void fetchHubJob<Job>(id).then(setJob).catch(() => undefined)} />
      {job && <JobLogModal job={job} scope="/hosts/local" onClose={() => setJob(null)} />}
    </>
  )
}
