package storage

func (s *Storage) Ping() error {
	return s.pool.Ping()
}
