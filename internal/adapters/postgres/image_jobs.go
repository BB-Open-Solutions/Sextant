package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
)

// ImageJobs exposes the imaging-execution store on the pool.
func (s *Store) ImageJobs() *ImageJobStore { return &ImageJobStore{s} }

// ImageJobStore implements ports.ImageJobStore.
type ImageJobStore struct{ s *Store }

// Upsert creates or replaces a job. A fresh dispatch resets status to the
// job's status (pending) and stamps created; a re-dispatch of the same MAC
// keeps the original created time.
func (j *ImageJobStore) Upsert(ctx context.Context, tenant string, job imaging.Job, now time.Time) error {
	status := job.Status
	if status == "" {
		status = imaging.Pending
	}
	_, err := j.s.pool.Exec(ctx, `
		INSERT INTO image_jobs (tenant, station, mac, tag, hardware, status, message, progress, step, created, updated)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
		ON CONFLICT (tenant, station, mac) DO UPDATE SET
			tag=EXCLUDED.tag, hardware=EXCLUDED.hardware, status=EXCLUDED.status,
			message=EXCLUDED.message, progress=EXCLUDED.progress, step=EXCLUDED.step, updated=EXCLUDED.updated`,
		tenant, job.Station, job.MAC, job.Tag, job.Hardware, string(status), job.Message, job.Progress, job.Step, now)
	if err != nil {
		return fmt.Errorf("upsert image job %s: %w", job.MAC, err)
	}
	return nil
}

// ListByStation returns every job for a station, newest first.
func (j *ImageJobStore) ListByStation(ctx context.Context, tenant, station string) ([]imaging.Job, error) {
	return j.query(ctx, `
		SELECT station, mac, tag, hardware, status, message, progress, step
		FROM image_jobs WHERE tenant=$1 AND station=$2 ORDER BY updated DESC`, tenant, station)
}

// ListPending returns jobs a station still has work for: not yet terminal.
func (j *ImageJobStore) ListPending(ctx context.Context, tenant, station string) ([]imaging.Job, error) {
	return j.query(ctx, `
		SELECT station, mac, tag, hardware, status, message, progress, step
		FROM image_jobs WHERE tenant=$1 AND station=$2 AND status IN ('pending','imaging')
		ORDER BY created`, tenant, station)
}

func (j *ImageJobStore) query(ctx context.Context, sql, tenant, station string) ([]imaging.Job, error) {
	rows, err := j.s.pool.Query(ctx, sql, tenant, station)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []imaging.Job
	for rows.Next() {
		var job imaging.Job
		var status string
		if err := rows.Scan(&job.Station, &job.MAC, &job.Tag, &job.Hardware, &status, &job.Message, &job.Progress, &job.Step); err != nil {
			return nil, err
		}
		job.Status = imaging.Status(status)
		out = append(out, job)
	}
	return out, rows.Err()
}

// Get returns one job by MAC, or false.
func (j *ImageJobStore) Get(ctx context.Context, tenant, station, mac string) (imaging.Job, bool, error) {
	var job imaging.Job
	var status string
	err := j.s.pool.QueryRow(ctx, `
		SELECT station, mac, tag, hardware, status, message, progress, step
		FROM image_jobs WHERE tenant=$1 AND station=$2 AND mac=$3`, tenant, station, mac).
		Scan(&job.Station, &job.MAC, &job.Tag, &job.Hardware, &status, &job.Message, &job.Progress, &job.Step)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return imaging.Job{}, false, nil
		}
		return imaging.Job{}, false, err
	}
	job.Status = imaging.Status(status)
	return job, true, nil
}

// UpdateProgress records how far the current step is (0..100) and its label,
// without changing status. It is the frequent, unguarded display-only tick.
func (j *ImageJobStore) UpdateProgress(ctx context.Context, tenant, station, mac string, progress int, step string, now time.Time) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	_, err := j.s.pool.Exec(ctx, `
		UPDATE image_jobs SET progress=$4, step=$5, updated=$6
		WHERE tenant=$1 AND station=$2 AND mac=$3`,
		tenant, station, mac, progress, step, now)
	return err
}

// TransitionStatus atomically moves a job from `from` to `to`: the WHERE
// clause pins the row's current status, so of two concurrent reports for the
// same job only the one that still finds `from` in the database matches a
// row - the loser's UPDATE affects zero rows instead of clobbering the
// winner's write. This closes the check-then-act race a separate Get +
// CanTransition + unconditional-write sequence would have at the application
// layer (there used to be such an unconditional UpdateStatus on this store;
// it was removed - every write to an existing job's status now goes through
// this guarded, conditional path). A status change starts a new step, so
// progress/step reset - otherwise a terminal record (installed/failed) would
// keep showing the in-progress percentage/label from whatever step it was
// last ticking.
func (j *ImageJobStore) TransitionStatus(ctx context.Context, tenant, station, mac string, from, to imaging.Status, message string, now time.Time) (bool, error) {
	tag, err := j.s.pool.Exec(ctx, `
		UPDATE image_jobs SET status=$5, message=$6, progress=0, step='', updated=$7
		WHERE tenant=$1 AND station=$2 AND mac=$3 AND status=$4`,
		tenant, station, mac, string(from), string(to), message, now)
	if err != nil {
		return false, fmt.Errorf("transition image job %s %s->%s: %w", mac, from, to, err)
	}
	return tag.RowsAffected() > 0, nil
}

// Delete removes a job.
func (j *ImageJobStore) Delete(ctx context.Context, tenant, station, mac string) error {
	_, err := j.s.pool.Exec(ctx,
		`DELETE FROM image_jobs WHERE tenant=$1 AND station=$2 AND mac=$3`, tenant, station, mac)
	return err
}
