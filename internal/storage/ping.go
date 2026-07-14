package storage

import "context"

func (s *Storage) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
