package comuse

// NewSyntheticSessionForTest exists only in the root test variant so external
// SDK/CLI integration tests can compose the real core with private synthetic
// desktop authority. It is absent from every production library and binary.
func NewSyntheticSessionForTest(config Config) (*Session, error) { return newSyntheticSession(config) }
