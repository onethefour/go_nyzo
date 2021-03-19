/*
A light node that follows the blockchain and archives it.
*/
package nyzo

import (
	"github.com/onethefour/go_nyzo/logging"
	"github.com/onethefour/go_nyzo/nyzo/configuration"
	"github.com/onethefour/go_nyzo/nyzo/data_store"
	"github.com/onethefour/go_nyzo/nyzo/interfaces"
)

type ArchiveNodeInterface interface {
	Start()
}

type archiveNodeState struct {
	ctxt *interfaces.Context
}

func (s *archiveNodeState) Start() {
	if err := configuration.EnsureSetup(); err != nil {
		logging.ErrorLog.Fatal(err.Error())
	}
	s.ctxt.SetRunMode(interfaces.RunModeArchive)
	s.ctxt.MeshListener = nil
	s.ctxt.DataStore = data_store.NewMysqlDataStore(s.ctxt)
	ContextInitialize(s.ctxt)
	ContextStart(s.ctxt)
	WaitForInterrupt()
	s.ctxt.WaitGroup.Wait()
}

// Create an archive node.
func NewArchiveNode() ArchiveNodeInterface {
	s := &archiveNodeState{}
	s.ctxt = NewDefaultContext()
	return s
}
