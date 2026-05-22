#!/bin/sh

set -e

command=$1
shift

case "$command" in
	_configure|_init)
		exit 0
		;;
esac

FailTask tail-log

case "$command" in
	update)
		say "Ok, I'll trigger the 'updatecfg' job to issue a git pull and reload configuration..."
		AddJob updatecfg
		FailTask say "Job failed!"
		AddTask say "... done"
		;;
	branch)
		branch=$1
		AddJob go-switchbranch "$branch"
		FailTask say "Error switching branches - does '$branch' exist?"
		FailTask tail-log
		AddTask send-message "... switched to branch '$branch'"
		;;
	*)
		exit $PLUGRET_NotFound
		;;
esac
