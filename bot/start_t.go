package bot

/*
start_t.go - non-interactive StartTest() function for automated "black box"
testing.
*/

import (
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"testing"
)

// Start a robot for testing, and return the exit / robot stopped channel
func StartTest(cfgdir, logfile string, t *testing.T) (<-chan struct{}, Connector) {
	wd, _ := os.Getwd()
	installpath := filepath.Dir(wd)
	configpath := filepath.Join(installpath, cfgdir)
	os.Setenv("GOPHER_INSTALLDIR", installpath)
	os.Setenv("GOPHER_CONFIGDIR", configpath)
	t.Logf("Initializing test bot with installpath: \"%s\" and configpath: \"%s\"", installpath, configpath)

	var botLogger *log.Logger
	if len(logfile) == 0 {
		botLogger = log.New(ioutil.Discard, "", 0)
	} else {
		lf, err := os.Create(logfile)
		if err != nil {
			log.Fatalf("Error creating log file: (%T %v)", err, err)
		}
		botLogger = log.New(lf, "", log.LstdFlags)
	}

	initBot(configpath, installpath, botLogger)

	initializeConnector, ok := connectors[robot.protocol]
	if !ok {
		botLogger.Fatalf("No connector registered with name: %s", robot.protocol)
	}

	// handler{} is just a placeholder struct for implementing the Handler interface
	h := handler{}
	conn := initializeConnector(h, botLogger)

	// NOTE: we use setConnector instead of passing the connector to run()
	// because of the way Windows services run. See 'start_win.go'.
	setConnector(conn)

	stopped := run()
	return stopped, conn
}