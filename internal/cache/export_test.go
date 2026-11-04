package cache

// SetRemoveAll replaces the function that Clear uses to remove the dir.
func SetRemoveAll(s *Store, f func(string) error) { s.removeAll = f }
