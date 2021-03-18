/*
The cycle authority is the ultimate arbiter over the cycle at the current frozen edge height.

The cycle authority reacts to the node's chosen bootstrap process, it can retrieve its own bootstrap info from other
nodes, or it can accept a pre-calculated set of info coming from, say, the block authority.
*/
package cycle_authority

import (
	"bytes"
	"go_nyzo/internal/logging"
	"go_nyzo/internal/nyzo/blockchain_data"
	"go_nyzo/internal/nyzo/configuration"
	"go_nyzo/internal/nyzo/interfaces"
	"go_nyzo/internal/nyzo/messages"
	"go_nyzo/internal/nyzo/messages/message_content"
	"go_nyzo/internal/nyzo/messages/message_content/message_fields"
	"go_nyzo/internal/nyzo/networking"
	"go_nyzo/internal/nyzo/router"
	"go_nyzo/internal/nyzo/utilities"
	"strconv"
	"sync"
	"time"
)

const (
	localMessageGetCurrentCycleLength  = -1
	localMessageVerifierInCurrentCycle = -2
	localMessageHasCycleAt             = -3
)

type state struct {
	ctxt                      *interfaces.Context
	cycleComplete             bool                           // do we know about a complete cycle?
	currentCycleEndHeight     int64                          // the end height of the current cycle
	currentCycle              [][]byte                       // the current cycle
	lastVerifierJoinHeight    int64                          // last time we saw a verifier join
	lastVerifierRemovalHeight int64                          // last time we saw a verifier leave
	isGenesisCycle            bool                           // are we currently in the genesis cycle?
	messageChannel            chan *messages.Message         // here's where we'll receive the messages we are registering for
	internalMessageChannel    chan *messages.InternalMessage // channel for internal and local messages
	winningBootstrapHash      []byte
	winningBootstrapHeight    int64
	winningBootstrapCycle     [][]byte
	chainInitialized          bool
	cycleBufferLock           sync.Mutex
	bufferTailHeight          int64
	bufferHeadHeight          int64
	cycleBuffer               [][]byte
}

// Length of the current cycle. This goes through the loop to make sure we can handle concurrency.
func (s *state) GetCurrentCycleLength() int {
	reply := router.GetInternalReply(localMessageGetCurrentCycleLength)
	return reply.Payload[0].(int)
}

// Is the given verifier currently in cycle? This goes through the loop to make sure we can handle concurrency.
func (s *state) VerifierInCurrentCycle(id []byte) bool {
	reply := router.GetInternalReply(localMessageVerifierInCurrentCycle, id)
	return reply.Payload[0].(bool)
}

// Is the given verifier currently in cycle? Can yield false positives during startup.
func (s *state) verifierInCurrentCycle(id []byte) bool {
	// startup phase, we assume that all verifiers are in cycle
	if len(s.currentCycle) == 0 {
		return true
	}
	for _, v := range s.currentCycle {
		if bytes.Equal(id, v) {
			return true
		}
	}
	return false
}

// Returns cycle information for the given block, calculating it first if necessary.
func (s *state) GetCycleInformationForBlock(block *blockchain_data.Block) *blockchain_data.CycleInformation {
	if block.CycleInformation != nil {
		return block.CycleInformation
	}
	cycleLengths := make([]int, 4, 4) // the length of up to 4 cycles, current one 1st.
	var (
		// those below can make it into the cycle information data
		maximumCycleLength, // the maximum length of ANY block in the past 3 cycles
		cycleIndex int // index in the above cycle length array
		foundNewVerifier, // did we find a new verifier with this current block?
		inGenesisCycle, // is this current block in the genesis cycle?
		// those below are used to control the loop locally
		reachedGenesisBlock,
		foundCycle,
		hasNewVerifier bool
		length int
	)

	currentBlock := block
	cycleStartHeight := block.Height
	for cycleIndex < 4 && !reachedGenesisBlock && currentBlock != nil {
		foundCycle, reachedGenesisBlock, hasNewVerifier, _, length = s.findCycleAt(currentBlock)
		if foundCycle {
			if cycleIndex == 0 {
				// only in the 1st cycle, these attributes can carry over to the block info
				inGenesisCycle = reachedGenesisBlock
				foundNewVerifier = hasNewVerifier
				if reachedGenesisBlock {
					// in the genesis cycle, we only see new verifiers
					foundNewVerifier = true
				}
			}
			// ANY block's cycle length
			if maximumCycleLength < length {
				maximumCycleLength = length
			}
			if currentBlock.Height == cycleStartHeight {
				// that's the primary cycle which we use to build the cycle length array
				cycleLengths[cycleIndex] = length
				cycleIndex++
				cycleStartHeight = currentBlock.Height - int64(length)
			}
			// step back one block
			currentBlock = s.ctxt.BlockFileHandler.GetBlock(currentBlock.Height - 1)
		} else {
			currentBlock = nil
		}
	}

	// special case "remnant" height if we reached the genesis block
	if reachedGenesisBlock && !inGenesisCycle && cycleIndex < 4 {
		cycleLengths[cycleIndex] = int(cycleStartHeight) + 1
	}

	// check if we have enough info to add cycle information to this block
	if cycleIndex == 4 || reachedGenesisBlock {
		block.CycleInformation = &blockchain_data.CycleInformation{
			MaximumCycleLength: maximumCycleLength,
			CycleLengths:       cycleLengths,
			NewVerifier:        foundNewVerifier,
			InGenesisCycle:     inGenesisCycle,
		}
	}

	return block.CycleInformation
}

// Verify continuity (diversity) rules for this block.
func (s *state) DetermineContinuityForBlock(block *blockchain_data.Block) int {
	//TODO: needs to be secured for concurrency
	if block.ContinuityState != blockchain_data.Undetermined {
		return block.ContinuityState
	}
	cycleInformation := s.GetCycleInformationForBlock(block)
	if cycleInformation != nil {
		// Proof-of-diversity rule 1: After the first existing verifier in the block chain, a new verifier is only
		// allowed if none of the other blocks in the cycle, the previous cycle, or the two blocks before the
		// previous cycle were verified by new verifiers.
		rule1Pass := false
		sufficientInformation := false
		if cycleInformation.InGenesisCycle || !cycleInformation.NewVerifier {
			rule1Pass = true
			sufficientInformation = true
		} else {
			startCheckHeight := block.Height - int64(cycleInformation.CycleLengths[0]) - int64(cycleInformation.CycleLengths[1]) - 1
			b := s.ctxt.BlockFileHandler.GetBlock(block.Height - 1)
			sufficientInformation = b != nil
			rule1Pass = true
			for b != nil && b.Height >= startCheckHeight && rule1Pass && sufficientInformation {
				if s.GetCycleInformationForBlock(b) == nil {
					sufficientInformation = false
				} else if b.CycleInformation.NewVerifier {
					rule1Pass = false
				}
				b = s.ctxt.BlockFileHandler.GetBlock(b.Height - 1)
				if b.Height > startCheckHeight && b == nil {
					sufficientInformation = false
				}
			}
		}
		if sufficientInformation && rule1Pass {
			// Proof-of-diversity rule 2: Past the Genesis block, the cycle of a block must be longer than half
			// of one more than the maximum of the all cycle lengths in this cycle and the previous two cycles.
			threshold := (cycleInformation.MaximumCycleLength + 1) / 2
			rule2Pass := block.Height == 0 || cycleInformation.CycleLengths[0] > threshold
			if rule2Pass {
				block.ContinuityState = blockchain_data.Valid
			} else {
				block.ContinuityState = blockchain_data.Invalid
			}
		} else {
			block.ContinuityState = blockchain_data.Invalid
		}
		// this looks like a good place to clean the cycle buffer
		s.cycleBufferLock.Lock()
		defer s.cycleBufferLock.Unlock()
		trailingEdge := block.CycleInformation.CalculateTrailingEdgeHeight(block.Height)
		// This would crap out if the genesis cycle were longer than 100 genesis verifiers. Unlikely.
		if s.bufferTailHeight < trailingEdge-100 {
			s.cycleBuffer = s.cycleBuffer[(trailingEdge-100)-s.bufferTailHeight:]
			s.bufferTailHeight += (trailingEdge - 100) - s.bufferTailHeight
		}
	}
	return block.ContinuityState
}

// Returns true if we know a cycle at the given block.
func (s *state) HasCycleAt(block *blockchain_data.Block) bool {
	reply := router.GetInternalReply(localMessageHasCycleAt, block)
	return reply.Payload[0].(bool)
}

// Utility to prepend y to x in the most memory efficient way possible.
func prependBytes(x [][]byte, y []byte) [][]byte {
	x = append(x, []byte{})
	copy(x[1:], x)
	x[0] = y
	return x
}

// Try to find a cycle starting at the given block, stepping backwards in the chain.
//
// Returns:
// found = did we find a full cycle? Example: a<-b<-a
// isGenesis = was it the genesis cycle? Meaning: did we reach block 0 without finding a full cycle?
// newVerifier = did the cycle start with a new verifier? Example: a<-b<-a<-c
// cycle = list of the verifier ids in the cycle Example: a, b, a, c
func (s *state) findCycleAt(startBlock *blockchain_data.Block) (found, isGenesis, newVerifier bool, cycle [][]byte, length int) {
	s.cycleBufferLock.Lock()
	defer s.cycleBufferLock.Unlock()
	if startBlock.CycleInfoCache != nil && startBlock.CycleInfoCache.CycleTailHeight >= s.bufferTailHeight && startBlock.CycleInfoCache.CycleHeadHeight <= s.bufferHeadHeight {
		cycle = s.cycleBuffer[startBlock.CycleInfoCache.CycleTailHeight-s.bufferTailHeight : int64(len(s.cycleBuffer))-(s.bufferHeadHeight-startBlock.CycleInfoCache.CycleHeadHeight)]
		return startBlock.CycleInfoCache.Found, startBlock.CycleInfoCache.IsGenesis, startBlock.CycleInfoCache.NewVerifier, cycle, len(cycle)
	}

	if startBlock.Height < s.bufferTailHeight || startBlock.Height > s.bufferHeadHeight+100 {
		// abandon a buffer that is probably not useful, should be rare
		s.cycleBuffer = make([][]byte, 0, 0)
		s.bufferHeadHeight = -1
	}

	if s.bufferHeadHeight >= 0 {
		// if we have a valid buffer, fill it up to the current head
		for i := s.bufferHeadHeight + 1; i <= startBlock.Height; i++ {
			var fillupBlock *blockchain_data.Block
			if i == startBlock.Height {
				fillupBlock = startBlock
			} else {
				fillupBlock = s.ctxt.BlockFileHandler.GetBlock(i)
			}
			if fillupBlock == nil {
				// gap in the chain, can't possible find a cycle
				return false, false, false, nil, 0
			}
			s.cycleBuffer = append(s.cycleBuffer, fillupBlock.VerifierIdentifier)
			s.bufferHeadHeight++
		}
	}

	if s.bufferHeadHeight == -1 {
		// if we don't have a valid buffer, start one
		s.cycleBuffer = append(s.cycleBuffer, startBlock.VerifierIdentifier)
		s.bufferHeadHeight = startBlock.Height
		s.bufferTailHeight = startBlock.Height
	}

	headHeight := startBlock.Height
	tailHeight := startBlock.Height
	for !found {
		if tailHeight >= 0 && tailHeight < s.bufferTailHeight {
			fillupBlock := s.ctxt.BlockFileHandler.GetBlock(tailHeight)
			if fillupBlock == nil {
				// gap in the chain, can't possible find a cycle
				return false, false, false, nil, 0
			}
			s.cycleBuffer = prependBytes(s.cycleBuffer, fillupBlock.VerifierIdentifier)
			s.bufferTailHeight = tailHeight
		}
		if headHeight != tailHeight {
			cycle = s.cycleBuffer[tailHeight-s.bufferTailHeight+1 : int64(len(s.cycleBuffer))-(s.bufferHeadHeight-headHeight)]
			if utilities.ByteArrayContains(cycle, s.cycleBuffer[tailHeight-s.bufferTailHeight]) {
				found = true
				newVerifier = !bytes.Equal(startBlock.VerifierIdentifier, s.cycleBuffer[tailHeight-s.bufferTailHeight])
				tailHeight++
			}
		}
		if tailHeight == 0 {
			newVerifier = true
			isGenesis = true
			found = true
		}
		if !found {
			tailHeight--
		}
	}
	if found {
		cycle = s.cycleBuffer[tailHeight-s.bufferTailHeight : int64(len(s.cycleBuffer))-(s.bufferHeadHeight-headHeight)]
		startBlock.CycleInfoCache = &blockchain_data.BlockCycleInfoCache{
			Found:           found,
			IsGenesis:       isGenesis,
			NewVerifier:     newVerifier,
			CycleHeadHeight: headHeight,
			CycleTailHeight: tailHeight,
		}
	}
	return found, isGenesis, newVerifier, cycle, len(cycle)
}

// Update the info about verifiers currently in cycle, with some fallback options if we can't find a full
// cycle going backwards from the current block.
func (s *state) updateVerifiersInCurrentCycle(currentBlock *blockchain_data.Block) {
	foundCycle, reachedGenesisBlock, isNewVerifier, cycle, _ := s.findCycleAt(currentBlock)

	var oldCycleLength int
	if s.currentCycle != nil {
		oldCycleLength = len(s.currentCycle)
	}

	if foundCycle {
		// we found a regular cycle
		s.currentCycle = cycle
		s.cycleComplete = true

		// If this is a new verifier and the height is greater than the previous value of lastVerifierJoinHeight,
		// store the height. This is used to cheaply determine whether new verifiers are eligible to join. The
		// greater-than condition is used to avoid issues that may arise during initialization.
		if isNewVerifier && currentBlock.Height > s.lastVerifierJoinHeight {
			s.lastVerifierJoinHeight = currentBlock.Height
			s.ctxt.PersistentData.Store(configuration.LastVerifierJoinHeightKey, strconv.FormatInt(currentBlock.Height, 10))
		}

		// If a verifier was dropped from the cycle, store the height. This is used to determine whether to
		// penalize poorly performing verifiers, as we do not want to drop verifiers from the cycle too quickly.
		if len(s.currentCycle) < oldCycleLength || (len(s.currentCycle) == oldCycleLength && isNewVerifier) {
			s.lastVerifierRemovalHeight = currentBlock.Height
			s.ctxt.PersistentData.Store(configuration.LastVerifierRemovalHeightKey, strconv.FormatInt(currentBlock.Height, 10))
		}

		// Store the edge height and indication of Genesis cycle.
		s.currentCycleEndHeight = currentBlock.Height
		s.isGenesisCycle = reachedGenesisBlock
	}

	if s.chainInitialized {
		logging.TraceLog.Printf("Updated cycle, old length: %d, new length: %d.", oldCycleLength, len(s.currentCycle))
	}
	//TODO: Java builds various sets here, probably for performance reasons, doesn't seem to make sense in Go, but we'll have to explore this whole list vs. map situation for the cycle at one time
}

/// Set cycle info directly: info obtained from a bootstrap response.
func (s *state) setBootstrapCycle(cycle [][]byte, height int64) {
	// Add bootstrap cycle to the cycle buffer.
	s.cycleBufferLock.Lock()
	s.cycleBuffer = make([][]byte, len(cycle))
	copy(s.cycleBuffer, cycle)
	s.bufferHeadHeight = height
	s.bufferTailHeight = height - int64(len(cycle)) + 1
	s.cycleBufferLock.Unlock()
	// Define current cycle.
	s.currentCycle = make([][]byte, len(cycle))
	copy(s.currentCycle, cycle)
	s.cycleComplete = true
}

// Send bootstrap request to either all managed verifiers (Sentinel), or to all trusted entry points (all other modes).
// The bootstrap response gives us info about the current frozen edge and the exact composition of the cycle.
// There are small differences here to Java, for the sake of better asynchronicity, but they should not have a
// tangible effect on the outcome of the bootstrap process.
func (s *state) bootstrapCycle() {
	bootstrapContent := message_content.NewBootstrapRequest(configuration.ListeningPortTcp)
	messageBootstrapRequest := messages.NewLocal(messages.TypeBootstrapRequest, bootstrapContent, s.ctxt.Identity)
	if s.ctxt.RunMode() == interfaces.RunModeSentinel || s.ctxt.RunMode() == interfaces.RunModeSentinel {
		nodes := s.ctxt.NodeManager.GetManagedVerifiers()
		for _, verifier := range nodes {
			go networking.FetchTcpNamed(messageBootstrapRequest, verifier.Host, verifier.Port)
		}
	} else {
		nodes := s.ctxt.NodeManager.GetTrustedEntryPoints()
		for _, entryPoint := range nodes {
			go networking.FetchTcpNamed(messageBootstrapRequest, entryPoint.Host, entryPoint.Port)
		}
	}
}

// Process a bootstrap response.
func (s *state) processBootstrapResponse(message *messages.Message) {
	bootstrapResponseContent := *message.Content.(*message_content.BootstrapResponse)
	if bootstrapResponseContent.CycleVerifiers == nil || len(bootstrapResponseContent.CycleVerifiers) == 0 {
		return
	}
	logging.TraceLog.Printf("Got bootstrap response from %s, frozen edge height: %d.", message_fields.IP4BytesToString(message.SourceIP), bootstrapResponseContent.FrozenEdgeHeight)
	if bootstrapResponseContent.FrozenEdgeHeight > s.winningBootstrapHeight {
		s.winningBootstrapHash = bootstrapResponseContent.FrozenEdgeHash
		s.winningBootstrapHeight = bootstrapResponseContent.FrozenEdgeHeight
		s.winningBootstrapCycle = bootstrapResponseContent.CycleVerifiers
	}
}

// Main loop
func (s *state) Start() {
	defer logging.InfoLog.Print("Main loop of cycle authority exited gracefully.")
	defer s.ctxt.WaitGroup.Done()
	logging.InfoLog.Print("Starting main loop of cycle authority.")
	bootstrapTicker := time.NewTicker(4 * time.Second)
	done := false
	for !done {
		select {
		case m := <-s.internalMessageChannel:
			switch m.Type {
			case localMessageGetCurrentCycleLength:
				m.ReplyChannel <- messages.NewInternalMessage(localMessageGetCurrentCycleLength, len(s.currentCycle))
			case localMessageVerifierInCurrentCycle:
				m.ReplyChannel <- messages.NewInternalMessage(localMessageVerifierInCurrentCycle, s.verifierInCurrentCycle(m.Payload[0].([]byte)))
			case localMessageHasCycleAt:
				foundCycle, _, _, _, _ := s.findCycleAt(m.Payload[0].(*blockchain_data.Block))
				m.ReplyChannel <- messages.NewInternalMessage(localMessageHasCycleAt, foundCycle)
			case messages.TypeInternalNewFrozenEdgeBlock:
				block := m.Payload[0].(*blockchain_data.Block)
				// for s.winningBootstrapHeight, we already know the cycle, for all others...
				if block.Height < s.winningBootstrapHeight {
					// Block authority decided to catch up, we abandon the bootstrap cycle.
					s.winningBootstrapHeight = 0
					s.currentCycle = make([][]byte, 0, 0)
					s.cycleComplete = false
					s.updateVerifiersInCurrentCycle(block)
				} else if block.Height > s.winningBootstrapHeight {
					// regular operation
					s.updateVerifiersInCurrentCycle(block)
				}
			case messages.TypeInternalChainInitialized:
				s.chainInitialized = true
			case messages.TypeInternalExiting:
				done = true
			}
		case m := <-s.messageChannel:
			switch m.Type {
			case messages.TypeBootstrapResponse:
				s.processBootstrapResponse(m)
			}
		case <-bootstrapTicker.C:
			if s.winningBootstrapHeight > 0 {
				s.setBootstrapCycle(s.winningBootstrapCycle, s.winningBootstrapHeight)
				router.Router.RouteInternal(messages.NewInternalMessage(messages.TypeInternalBootstrapBlock, s.winningBootstrapHeight, s.winningBootstrapHash))
				logging.InfoLog.Printf("Cycle authority exited bootstrap phase, consensus cycle length: %d, consensus frozen edge: %d.", len(s.winningBootstrapCycle), s.winningBootstrapHeight)
				bootstrapTicker.Stop()
			} else if s.ctxt.RunMode() != interfaces.RunModeArchive {
				s.bootstrapCycle()
			} else {
				bootstrapTicker.Stop()
			}
		}
	}
}

// Initialization function
func (s *state) Initialize() error {
	// set message routes
	s.messageChannel = make(chan *messages.Message, 20)
	router.Router.AddRoute(messages.TypeBootstrapResponse, s.messageChannel)
	s.internalMessageChannel = make(chan *messages.InternalMessage, 150)
	router.Router.AddInternalRoute(messages.TypeInternalNewFrozenEdgeBlock, s.internalMessageChannel)
	router.Router.AddInternalRoute(messages.TypeInternalExiting, s.internalMessageChannel)
	router.Router.AddInternalRoute(messages.TypeInternalChainInitialized, s.internalMessageChannel)
	router.Router.AddInternalRoute(localMessageGetCurrentCycleLength, s.internalMessageChannel)
	router.Router.AddInternalRoute(localMessageVerifierInCurrentCycle, s.internalMessageChannel)
	router.Router.AddInternalRoute(localMessageHasCycleAt, s.internalMessageChannel)
	s.currentCycle = make([][]byte, 0, 0)
	s.cycleBuffer = make([][]byte, 0, 0)
	s.bufferHeadHeight = -1
	return nil
}

// Create a cycle authority.
func NewCycleAuthority(ctxt *interfaces.Context) interfaces.CycleAuthorityInterface {
	s := &state{}
	s.ctxt = ctxt
	return s
}
