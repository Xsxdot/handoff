package agentd

// TaskNoticeSourceMachine returns the stable local source key used by task
// notification projections. Named machines use their configured identity;
// nameless local agents retain the hostname-based mirror identity.
func (s *Server) TaskNoticeSourceMachine() string {
	if cfg := s.conf(); cfg != nil && cfg.Name != "" {
		return cfg.Name
	}
	return s.localMachineName()
}
