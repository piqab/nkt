package com.netknownsthat.domain.usecase

import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.common.JobOwner
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.model.JobLogLine
import com.netknownsthat.domain.model.JobRecord
import com.netknownsthat.domain.repository.JobsRepository
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlin.time.Duration
import kotlin.time.Duration.Companion.milliseconds

/** One step of a followed job: its state and the lines that arrived. */
data class JobProgress(
    val job: JobRecord?,
    val newLines: List<JobLogLine>,
    /** Set while the hub cannot be reached; following continues. */
    val error: AppError? = null,
)

/**
 * Follows a job's log until it finishes — the incremental read
 * (/jobs/{id}/log?after=N) the hub itself uses to follow a host's job.
 */
class WatchJobUseCase(
    private val jobs: JobsRepository,
    private val poll: Duration = 1_500.milliseconds,
) {
    operator fun invoke(owner: JobOwner, jobId: Long): Flow<JobProgress> = flow {
        var after = 0L
        while (true) {
            when (val r = jobs.log(owner, jobId, after)) {
                is Outcome.Success -> {
                    r.value.lines.forEach { after = maxOf(after, it.seq) }
                    emit(JobProgress(r.value.job, r.value.lines))
                    if (r.value.job.finished && r.value.lines.isEmpty()) return@flow
                }
                is Outcome.Failure -> emit(JobProgress(null, emptyList(), r.error))
            }
            delay(poll)
        }
    }
}
