/*
A Nyzo verifier.
*/
package nyzo

import (
	"github.com/onethefour/go_nyzo/logging"
	"github.com/onethefour/go_nyzo/nyzo/configuration"
	"github.com/onethefour/go_nyzo/nyzo/interfaces"
)

type VerifierInterface interface {
	Start()
}

type verifierState struct {
	ctxt *interfaces.Context
}

func (s *verifierState) Start() {
	if err := configuration.EnsureSetup(); err != nil {
		logging.ErrorLog.Fatal(err.Error())
	}
	ContextInitialize(s.ctxt)
	ContextStart(s.ctxt)
	WaitForInterrupt()
	s.ctxt.WaitGroup.Wait()
}

// Create a verifier.
func NewVerifier() VerifierInterface {
	s := &verifierState{}
	s.ctxt = NewDefaultContext()
	return s
}
