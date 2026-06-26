#!/usr/bin/python3

import sys
from gopherbot_v2 import Robot

default_config = """
---
AllChannels: true
AllowedPrivateCommands:
- "*"
Commands:
- SimpleMatcher: python-pipeline
  Command: pipeline
"""

if len(sys.argv) < 2:
    sys.exit(1)

sys.argv.pop(0)
command = sys.argv.pop(0)

if command == "_configure":
    print(default_config)
    sys.exit(0)
if command == "_init":
    sys.exit(0)

bot = Robot()

if command == "pipeline":
    ret = bot.AddTask("python-pipeline-task", ["set-python"])
    if ret == Robot.Ok:
        ret = bot.AddTask("python-pipeline-task", ["check-python"])
    if ret == Robot.Ok:
        bot.Say("PYTHON PIPELINE: queued")
        sys.exit(Robot.Normal)
    bot.Say("PYTHON PIPELINE: add-task failed")
    sys.exit(Robot.Fail)

if command == "set-python":
    if bot.SetParameter("PIPELINE_LANGUAGE", "python"):
        bot.Say("PYTHON PIPELINE TASK: set parameter python")
        sys.exit(Robot.Normal)
    bot.Say("PYTHON PIPELINE TASK: set parameter failed")
    sys.exit(Robot.Fail)

if command == "check-python":
    value = bot.GetParameter("PIPELINE_LANGUAGE")
    if value == "python":
        bot.Say("PYTHON PIPELINE TASK: checked parameter python")
        sys.exit(Robot.Normal)
    bot.Say("PYTHON PIPELINE TASK: checked parameter failed")
    sys.exit(Robot.Fail)

sys.exit(1)
