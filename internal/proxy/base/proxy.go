package base

import (
	"crypto/tls"
	"fmt"
	"sync"
	"time"

	"github.com/D00Movenok/BounceBack/internal/common"
	"github.com/D00Movenok/BounceBack/internal/database"
	"github.com/D00Movenok/BounceBack/internal/filters"
	"github.com/D00Movenok/BounceBack/internal/wrapper"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	defaultTimeout = time.Second * 10
)

func NewBaseProxy(
	cfg common.ProxyConfig,
	fs *filters.FilterSet,
	db *database.DB,
	actions []string,
) (*Proxy, error) {
	logger := log.With().
		Str("proxy", cfg.Name).
		Logger()

	err := verifyAction(cfg.FilterSettings.Action, actions)
	if err != nil {
		return nil, err
	}

	for _, f := range cfg.Filters {
		_, ok := fs.Get(f)
		if !ok {
			return nil, fmt.Errorf(
				"can't find filter \"%s\" for proxy \"%s\"",
				f,
				cfg.Name,
			)
		}
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
		logger.Debug().Msgf(
			"Using default timeout: %s",
			cfg.Timeout,
		)
	}

	base := &Proxy{
		Config: cfg,

		Closing: false,
		Logger:  logger,

		db:      db,
		filters: fs,
	}

	if cfg.TLS != nil {
		var cert tls.Certificate
		cert, err = tls.LoadX509KeyPair(cfg.TLS.Cert, cfg.TLS.Key)
		if err != nil {
			return nil, fmt.Errorf("can't load tls config: %w", err)
		}
		// #nosec G402
		base.TLSConfig = &tls.Config{
			Certificates:       []tls.Certificate{cert},
			InsecureSkipVerify: true,
		}
	}

	return base, nil
}

type Proxy struct {
	Config    common.ProxyConfig
	TLSConfig *tls.Config

	Closing bool
	WG      sync.WaitGroup
	Logger  zerolog.Logger

	db      *database.DB
	filters *filters.FilterSet
}

func (p *Proxy) GetLogger() *zerolog.Logger {
	logger := p.Logger.With().
		Str("listen", p.Config.ListenAddr).
		Str("target", p.Config.TargetAddr).
		Str("type", p.Config.Type).
		Logger()
	return &logger
}

// Return true if entity passed all checks and false if filtered.
func (p *Proxy) RunFilters(e wrapper.Entity, logger zerolog.Logger) bool {
	ip := e.GetIP().String()

	if p.isRejectedByThreshold(ip, logger) {
		return false
	}

	mg := p.prepareFilters(e, logger)

	// TODO: cache filters for equal entities for optimization.
	// TODO: add accept verdict.
	for i, f := range p.Config.Filters {
		mg[i].Lock()
		defer mg[i].Unlock()

		filterLogger := logger.With().Str("filter", f).Logger()
		filter, _ := p.filters.Get(f)
		filtered, err := filter.Apply(e, filterLogger)
		if err != nil {
			filterLogger.Error().Err(err).Msg("Filter error, skipping...")
			continue
		}
		if filtered {
			filterLogger.Warn().Msg("Filtered")
			err = p.db.IncRejects(ip)
			if err != nil {
				logger.Error().Err(err).Msg("Can't increase rejects")
			}
			return false
		}
	}

	err := p.db.IncAccepts(ip)
	if err != nil {
		logger.Error().Err(err).Msg("Can't increase accepts")
	}

	return true
}

// check NoRejectThreshold and RejectThreshold.
// return true if rejected by RejectThreshold, otherwise false.
func (p *Proxy) isRejectedByThreshold(ip string, logger zerolog.Logger) bool {
	v, err := p.db.GetVerdict(ip)
	if err != nil {
		v = &database.Verdict{}
		logger.Error().Err(err).Msg("Can't get cached verdict")
	}
	switch {
	case p.Config.FilterSettings.NoRejectThreshold > 0 &&
		v.Accepts >= p.Config.FilterSettings.NoRejectThreshold:
	case p.Config.FilterSettings.RejectThreshold > 0 &&
		v.Rejects >= p.Config.FilterSettings.RejectThreshold:
		logger.Warn().Msg("Rejected permanently")
		return true
	default:
	}

	return false
}

// run all requests (e.g. DNS PTR, GEO) concurently for optimisation.
func (p *Proxy) prepareFilters(
	e wrapper.Entity,
	logger zerolog.Logger,
) []sync.Mutex {
	mg := make([]sync.Mutex, len(p.Config.Filters))
	for i, f := range p.Config.Filters {
		go func(index int, ff string) {
			mg[index].Lock()
			defer mg[index].Unlock()

			filterLogger := logger.With().Str("filter", ff).Logger()
			filter, _ := p.filters.Get(ff)
			err := filter.Prepare(e, filterLogger)
			if err != nil {
				filterLogger.Error().Err(err).Msg("Prepare error, skipping...")
			}
		}(i, f)
	}
	return mg
}

func (p *Proxy) String() string {
	return fmt.Sprintf("%s proxy \"%s\" (%s->%s)",
		p.Config.Type, p.Config.Name, p.Config.ListenAddr, p.Config.TargetAddr)
}
