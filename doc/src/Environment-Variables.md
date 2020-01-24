# Gopherbot Environment Variables

**Gopherbot** makes extensive use of environment variables, both for configuring the robot and plugins, and for providing parameters to external scripts. This article describes the various environment variables and their use; for the environment applicable to a given running task, see [per-task environment](pipelines/TaskEnvironment.md).

## Robot Execution Environment

Certain environment variables can be supplied to the running **Gopherbot** process to configure and/or bootstrap your robot. These environment variables can be set by:

* `systemd` - Not recommded; while systemd can provide environment variables to your robot, it's insecure and will allow local users on the system to view the values
* `$GOPHER_HOME/private/environment` - a slightly better option, normally used for devel robots with `fetch-robot.sh`, where `private` is a private repository with the single file `environment`
* `docker` or `docker-compose` - these and other container environments provide more secure means of providing environment variables to containers
* `$GOPHER_HOME/.env` - the most secure means is by creating a `.env` in `$GOPHER_HOME`, outside of any git repository, mode `0600`

The last two options are recommended for production deployments of a **Gopherbot** robot.

### Start-up Environment

The following values can be provided to your robot on start-up:

* `GOPHER_ENCRYPTION_KEY` - 32+ character encryption key used for decrypting the `binary-encrypted-key`
* `GOPHER_CUSTOM_REPOSITORY` - clone URL for the robot's custom configuration, used in bootstrapping
* `GOPHER_CUSTOM_BRANCH` - branch to use if other than `master`
* `GOPHER_LOGFILE` - where to write out a log file
* `GOPHER_CONFIGDIR` - absolute or relative path to configuration directory
* `DEPLOY_KEY` - ssh deploy key for cloning the custom repository

For the optional `state` and `private` repositories, the included jobs will use the `GOPHER_CUSTOM_REPOSITORY` value with `s/gopherbot/state/` and `s/gopherbot/private/` (same branch). If desired, the values can also be supplied:
* `GOPHER_STATE_REPOSITORY` - repository holding state, normally just a file-backed brain
* `GOPHER_STATE_BRANCH` - branch to use if other than `master`
* `GOPHER_PRIVATE_REPOSITORY` - non-public repository with `environment`, for dev only
* `GOPHER_PRIVATE_BRANCH` - branch to use if other than `master`

### Configuration Environment Variables

**Gopherbot** normally takes almost all of it's configuration from the collection of `*.yaml` files in the custom configuration directory, but for easy flexibility, a collection of environment variables are referenced in the default configuration. These are some of the values that are expanded; the actual configuration files are the definitive reference.

* `GOPHER_PROTOCOL` - used to select a non-default protocol (e.g. "terminal")
* `GOPHER_LOGLEVEL` - error, warn, info, debug, trace
* `GOPHER_BOTNAME` - the name the robot will answer to, e.g. "floyd"
* `GOPHER_ALIAS` - the one-character alias for the robot, e.g. ";"
* `GOPHER_BOTMAIL` - the robot's email address
* `GOPHER_BOTFULLNAME` - the robot's full name
* `GOPHER_HISTORY_DIRECTORY` - directory for storing file-based historical job logs
* `GOPHER_WORKSPACE_DIRECTORY` - workspace directory where e.g. build jobs clone and run
* `GOPHER_BRAIN` - non-default brain provider to use
* `GOPHER_STATE_DIRECTORY` - default dir for storing state, normally just the brain
* `GOPHER_BRAIN_DIRECTORY` - directory where file-based memories are stored, overrides above
* `GOPHER_JOBCHANNEL` - where jobs run by default if not otherwise specified
* `GOPHER_TIMEZONE` - UNIX tz, e.g. "America/New_York" (default)

## External Script Environment

**Gopherbot** always scrubs the environment when executing tasks, so environment variables set on execution are not automatically passed to child processes. The only environment variables that are passed through from original execution are:
* `HOME` - this should rarely be used; for portable robots, use `GOPHER_HOME`, instead
* `HOSTNAME`
* `LANG`
* `PATH` - this should be used with care since it can make your robot less portable
* `USER`

In addition to the above passed-through environment vars, **Gopherbot** supplies the following environment variables to external scripts:
* `GOPHER_INSTALLDIR` - absolute path to the gopherbot install, normally `/opt/gopherbot`

## Automatic Environment Variables

On startup, **Gopherbot** will check for a custom `conf/gopherbot.yaml` or the presence of a `GOPHER_CUSTOM_REPOSITORY` environment variable. In the absence of either, the following will be automatically set:
* `GOPHER_UNCONFIGURED` - set true
* `GOPHER_PROTOCOL` - set to "terminal" if not already set
* `GOPHER_LOGFILE` - set to "robot.log" if not already set
Additionally, `GOPHER_ENCRYPTION_INITIALIZED` will be set to `true` if encryption is initialized on start-up, regardless of whether the robot is configured.

## Pipeline Environment Variables
The following environment variable are set for all pipelines, whether started by a plugin or a job:
* `GOPHER_CHANNEL` - the channel where the plugin/job is providing output
* `GOPHER_USER` - the user whose message created the pipeline (if any)
* `GOPHER_PROTOCOL` - the name of the protocol in use, e.g. "slack"
* `GOPHER_TASK_NAME` - the name of the running task
* `GOPHER_PIPELINE_TYPE` - the event type that started the current pipeline, one of:
    * `plugCommand` - direct robot command, not `run job ...`
    * `plugMessage` - ambient message matched
    * `catchAll` - catchall plugin ran
    * `jobTrigger` - triggered by a JobTrigger
    * `scheduled` - started by a ScheduledTask
    * `jobCommand` - started from `run job ...` command

The following are also supplied whenever a job is run:
* `GOPHER_JOB_NAME` - the name of the running job
* `GOPHER_START_CHANNEL` - the channel where the job was started
* `GOPHER_REPOSITORY` - the extended namespace from `repositories.yaml`, if any
* `GOPHER_RUN_INDEX` - the run number of the job

Pipelines and tasks that have `Homed: true` and/or `Privileged: true` may also get:
* `GOPHER_HOME` - absolute path to the startup directory for the robot, relative paths are relative to this directory; unset if `cwd` can't be determined
* `GOPHER_WORKSPACE` - the workspace directory (normally relative to `GOPHER_HOME`)
* `GOPHER_CONFIGDIR` - absolute path to custom configuration directory, normally `$GOPHER_HOME/custom`
* `GOPHER_WORKDIR` - set to the current working directory for the pipeline (used by e.g. the "clean" task)

### GopherCI Environment Variables

In addition to the environment variables set by the **Gopherbot** engine, the `localbuild` GopherCI builder sets the following environment variables that can be used to modify pipelines:
* `GOPHERCI_BRANCH` - the branch being built (`GOPHER_REPOSITORY` is set by `ExtendNamespace`)
* `GOPHERCI_DEPBUILD` - set to "true" if the build was triggered by a dependency
* `GOPHERCI_DEPREPO` - the updated repository that triggered this build
* `GOPHERCI_DEPBRANCH` - the updated branch