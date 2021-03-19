/*
A node that protects in-cycle verifiers and cycle joins.
*/
package nyzo

import (
	"github.com/onethefour/go_nyzo/logging"
	"github.com/onethefour/go_nyzo/nyzo/configuration"
	"github.com/onethefour/go_nyzo/nyzo/interfaces"
)

type SentinelInterface interface {
	Start()
}

type sentinelState struct {
	ctxt *interfaces.Context
}

func (s *sentinelState) Start() {
	if err := configuration.EnsureSetup(); err != nil {
		logging.ErrorLog.Fatal(err.Error())
	}
	s.ctxt.SetRunMode(interfaces.RunModeSentinel)
	s.ctxt.MeshListener = nil
	ContextInitialize(s.ctxt)
	ContextStart(s.ctxt)
	WaitForInterrupt()
	s.ctxt.WaitGroup.Wait()
}

// Create a sentinel.
func NewSentinel() SentinelInterface {
	s := &sentinelState{}
	s.ctxt = NewDefaultContext()
	return s
}
