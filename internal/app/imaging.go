package app

import (
	"context"
	"fmt"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// ImagingService is the application surface of the imaging-execution plane:
// an operator dispatches image jobs, an imaging station claims and reports
// them. Transition rules live in the domain; this wires them to the store for
// one tenant namespace.
type ImagingService struct {
	store  ports.ImageJobStore
	clock  ports.Clock
	tenant string
}

// NewImagingService wires the imaging-execution plane for one tenant.
func NewImagingService(store ports.ImageJobStore, clock ports.Clock, tenant string) *ImagingService {
	if tenant == "" {
		tenant = DefaultTenant
	}
	return &ImagingService{store: store, clock: clock, tenant: tenant}
}

// Dispatch records (or re-records) an image job in the pending state after
// validating it. Re-dispatching the same MAC replaces the prior job.
func (s *ImagingService) Dispatch(ctx context.Context, job imaging.Job) error {
	job.MAC = imaging.NormalizeMAC(job.MAC)
	if job.Status == "" {
		job.Status = imaging.Pending
	}
	if err := job.Validate(); err != nil {
		return err
	}
	return s.store.Upsert(ctx, s.tenant, job, s.clock.Now())
}

// List returns every job for a station, newest first.
func (s *ImagingService) List(ctx context.Context, station string) ([]imaging.Job, error) {
	return s.store.ListByStation(ctx, s.tenant, station)
}

// Pending returns the jobs a station still has work for (the poll).
func (s *ImagingService) Pending(ctx context.Context, station string) ([]imaging.Job, error) {
	return s.store.ListPending(ctx, s.tenant, station)
}

// Get returns one job by MAC.
func (s *ImagingService) Get(ctx context.Context, station, mac string) (imaging.Job, bool, error) {
	return s.store.Get(ctx, s.tenant, station, imaging.NormalizeMAC(mac))
}

// Report moves a job to a new status, enforcing the domain transition rules
// (a station cannot report a status a job cannot reach). Message carries
// failure detail; it is cleared on a non-failure transition.
func (s *ImagingService) Report(ctx context.Context, station, mac string, to imaging.Status, message string) error {
	mac = imaging.NormalizeMAC(mac)
	job, ok, err := s.store.Get(ctx, s.tenant, station, mac)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no image job for %s on station %s", mac, station)
	}
	if !job.Status.CanTransition(to) {
		return fmt.Errorf("image job %s cannot go from %s to %s", mac, job.Status, to)
	}
	if to != imaging.Failed {
		message = ""
	}
	return s.store.UpdateStatus(ctx, s.tenant, station, mac, to, message, s.clock.Now())
}

// Cancel withdraws a job the operator no longer wants imaged.
func (s *ImagingService) Cancel(ctx context.Context, station, mac string) error {
	return s.Report(ctx, station, mac, imaging.Canceled, "")
}

// Remove deletes a job record (once installed and reconciled, or abandoned).
func (s *ImagingService) Remove(ctx context.Context, station, mac string) error {
	return s.store.Delete(ctx, s.tenant, station, imaging.NormalizeMAC(mac))
}
