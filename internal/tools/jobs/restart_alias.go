package jobs

// CurrentIDForSession resolves a restarted job id to the attempt still carrying
// its work, so a caller holding the old id can keep reading the replacement.
func (m *Manager) CurrentIDForSession(parentSession, id string) (string, bool) {
	j := m.get(parentSession, id)
	if j == nil {
		return "", false
	}
	return j.ID, true
}

func (m *Manager) followReplacementLocked(parentSession, id string) *Job {
	j := m.findJobLocked(parentSession, id)
	seen := map[string]bool{}
	for j != nil && !seen[j.ID] {
		seen[j.ID] = true
		j.mu.Lock()
		next := j.launch.replacedBy
		j.mu.Unlock()
		if next == "" {
			return j
		}
		replacement := m.findJobLocked(parentSession, next)
		if replacement == nil {
			return j
		}
		j = replacement
	}
	return j
}

// resolve maps requested ids to their current attempts; an empty list selects
// every running job. A restart alias therefore keeps wait and output attached
// to the work still running under the replacement id.
func (m *Manager) resolve(parentSession string, ids []string) []*Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Job
	seen := map[string]bool{}
	if len(ids) == 0 {
		for _, key := range m.order {
			j := m.jobs[key]
			if !sessionMatches(parentSession, j.SessionID) {
				continue
			}
			j.mu.Lock()
			running := j.status == Running
			j.mu.Unlock()
			if running {
				out = append(out, j)
			}
		}
		return out
	}
	for _, id := range ids {
		if j := m.followReplacementLocked(parentSession, id); j != nil && !seen[j.ID] {
			seen[j.ID] = true
			out = append(out, j)
		}
	}
	return out
}
