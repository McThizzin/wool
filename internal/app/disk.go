package app

import "wool/internal/discovery"

// refreshDisk rebuilds the stat-only model inventory. It runs on tab entry and
// after a config reload, never with full content hashing, so it stays cheap.
func (m *Model) refreshDisk() {
	m.disk = discovery.Disk(m.cfg, discovery.DiskOptions{SkipContent: true})
}
