package loginattemptimpl

import (
	"context"
	"time"

	"github.com/grafana/grafana/pkg/infra/db"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/infra/serverlock"
	"github.com/grafana/grafana/pkg/setting"
)

const (
	maxInvalidLoginAttempts int64 = 5
	loginAttemptsWindow           = time.Minute * 5
)

func ProvideService(db db.DB, cfg *setting.Cfg, lock *serverlock.ServerLockService) *Service {
	return &Service{
		&xormStore{db: db, now: time.Now},
		cfg,
		lock,
		log.New("login_attempt"),
	}
}

type Service struct {
	store  store
	cfg    *setting.Cfg
	lock   *serverlock.ServerLockService
	logger log.Logger
}

func (s *Service) Run(ctx context.Context) error {
	// no need to run clean up job if it is disabled
	if s.cfg.DisableBruteForceLoginProtection {
		return nil
	}

	ticker := time.NewTicker(time.Minute * 10)
	for {
		select {
		case <-ticker.C:
			s.cleanup(ctx)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Service) Add(ctx context.Context, username, IPAddress string) error {
	if s.cfg.DisableBruteForceLoginProtection {
		return nil
	}

	return s.store.CreateLoginAttempt(ctx, CreateLoginAttemptCommand{
		Username:  username,
		IpAddress: IPAddress,
	})
}

func (s *Service) Validate(ctx context.Context, username string) (bool, error) {
	if s.cfg.DisableBruteForceLoginProtection {
		return true, nil
	}

	loginAttemptCountQuery := GetUserLoginAttemptCountQuery{
		Username: username,
		Since:    time.Now().Add(-loginAttemptsWindow),
	}

	count, err := s.store.GetUserLoginAttemptCount(ctx, loginAttemptCountQuery)
	if err != nil {
		return false, err
	}

	if count >= maxInvalidLoginAttempts {
		return false, nil
	}

	return true, nil
}

func (s *Service) cleanup(ctx context.Context) {
	err := s.lock.LockAndExecute(ctx, "delete old login attempts", time.Minute*10, func(context.Context) {
		cmd := DeleteOldLoginAttemptsCommand{
			OlderThan: time.Now().Add(time.Minute * -10),
		}
		if deletedLogs, err := s.store.DeleteOldLoginAttempts(ctx, cmd); err != nil {
			s.logger.Error("Problem deleting expired login attempts", "error", err.Error())
		} else {
			s.logger.Debug("Deleted expired login attempts", "rows affected", deletedLogs)
		}
	})

	if err != nil {
		s.logger.Error("failed to lock and execute cleanup of old login attempts", "error", err)
	}
}
