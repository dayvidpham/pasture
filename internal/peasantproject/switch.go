package peasantproject

import (
	"context"
	"errors"
	"sync"
)

// ErrSwitchClosed reports a settings change after the serve process began
// shutting down.
var ErrSwitchClosed = errors.New("the Peasant project toggle is closed; restart pasture serve to change project enrichment")

// DisabledMessage is the authoritative integration message while the runtime
// switch is off. It names the console toggle first and keeps the startup flags
// as the fallback for a non-default source.
const DisabledMessage = "Peasant project enrichment is off; turn it on from the web console, or restart pasture serve with --peasant-db/--peasant-url for a non-default source"

// Switch is the runtime on/off control for the opt-in Peasant project source.
//
// It starts disabled: no source is touched until the operator enables it. A
// lazy opener is called on the first enable; disabling closes and clears the
// source, and later lookups do nothing. Status while off is SourceDisabled with
// an empty State, never StateUnavailable.
type Switch struct {
	mu     sync.Mutex
	opener func(context.Context) (Source, error)
	source Source
	on     bool
	closed bool
}

// NewSwitch builds a switch. A non-nil initial source starts the switch on
// (the explicit --peasant-db/--peasant-url path); a nil source starts it off
// and defers all access to opener.
func NewSwitch(opener func(context.Context) (Source, error), initial Source) *Switch {
	s := &Switch{opener: opener}
	if initial != nil {
		s.source = initial
		s.on = true
	}
	return s
}

// Status reports the integration state. While off it is disabled, distinct
// from an enabled source that cannot answer.
func (s *Switch) Status() Status {
	s.mu.Lock()
	if !s.on || s.source == nil {
		s.mu.Unlock()
		return Status{Enabled: false, Source: SourceDisabled, Message: DisabledMessage}
	}
	source := s.source
	s.mu.Unlock()
	status := source.Status()
	status.Enabled = true
	return status
}

// Lookup resolves projects only while the switch is on. While off it returns an
// empty mapping and never touches the source.
func (s *Switch) Lookup(ctx context.Context, keys []Key) (map[Key]Project, error) {
	s.mu.Lock()
	if !s.on || s.source == nil {
		s.mu.Unlock()
		return make(map[Key]Project), nil
	}
	source := s.source
	s.mu.Unlock()
	return source.Lookup(ctx, keys)
}

// SetEnabled turns enrichment on or off. Off closes and clears the open source;
// on calls the lazy opener when no source is open and reports an actionable
// error (leaving the switch off) when it cannot be created.
func (s *Switch) SetEnabled(ctx context.Context, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSwitchClosed
	}
	if !enabled {
		source := s.source
		s.source = nil
		s.on = false
		if source == nil {
			return nil
		}
		// The switch is already off. A failure to close the old handle must not
		// report a 409 for a state the operator successfully requested; the
		// source is unreachable from here either way.
		if closer, ok := source.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return nil
	}
	if s.on && s.source != nil {
		return nil
	}
	if s.opener == nil {
		return errors.New("this serve process has no configurable Peasant project source; restart pasture serve with --peasant-db or --peasant-url")
	}
	source, err := s.opener(ctx)
	if err != nil {
		s.source = nil
		s.on = false
		return err
	}
	if source == nil {
		s.source = nil
		s.on = false
		return errors.New("the Peasant project source opener returned no source; enrichment stays off")
	}
	s.source = source
	s.on = true
	return nil
}

// Close releases any open source and is safe to call more than once.
func (s *Switch) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	source := s.source
	s.source = nil
	s.on = false
	if source == nil {
		return nil
	}
	if closer, ok := source.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
