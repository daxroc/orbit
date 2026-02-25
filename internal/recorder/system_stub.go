//go:build !linux

package recorder

import (
	"time"
)

type SystemRecorder struct {
	nodeName string
}

func NewSystemRecorder(nodeName string) *SystemRecorder {
	return &SystemRecorder{nodeName: nodeName}
}

func (s *SystemRecorder) Start(_ time.Duration) {}
func (s *SystemRecorder) Stop()                 {}
