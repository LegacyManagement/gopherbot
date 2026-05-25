package bot

const startupGateMessage = "(the robot is still starting up, please wait and try your command again later)"

func resetStartupGate() {
	state.Lock()
	state.startingUp = true
	state.Unlock()
}

func releaseStartupGate() {
	state.Lock()
	state.startingUp = false
	state.Unlock()
}

func isStartupGateClosed() bool {
	state.RLock()
	starting := state.startingUp
	state.RUnlock()
	return starting
}
