package bot

// Handle SIGINT and SIGTERM with a graceful shutdown

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func init() {
	sigs := make(chan os.Signal, 1)

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		shutdownMutex.Lock()
		shuttingDown = true
		shutdownMutex.Unlock()
		Log(Info, fmt.Sprintf("Received signal: %s, shutting down gracefully", sig))
		// Wait for all plugins to stop running
		plugRunningWaitGroup.Wait()
		// Get the dataLock to make sure the brain is in a consistent state
		dataLock.Lock()
		Log(Info, fmt.Sprintf("Exiting on signal: %s", sig))
		os.Exit(0)
	}()
}