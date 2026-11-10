package watch

// inotify has no event ids.
func currentEventID() (uint64, bool) { return 0, false }
