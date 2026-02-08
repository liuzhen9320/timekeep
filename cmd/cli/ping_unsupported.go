//go:build !windows && !linux

package main

func (s *CLIService) StatusService() error {
	return nil
}

// GetServiceStatusString returns the service status as a string
func (s *CLIService) GetServiceStatusString() (string, error) {
	return "unsupported", nil
}
