#!/usr/bin/ruby

require 'gopherbot_v1'

default_config = <<'DEFCONFIG'
---
AllChannels: true
AllowedPrivateCommands:
- "*"
Commands:
- SimpleMatcher: ruby-pipeline
  Command: pipeline
DEFCONFIG

command = ARGV.shift

case command
when "_configure"
  puts default_config
  exit 0
when "_init"
  exit 0
end

bot = Robot.new

case command
when "pipeline"
  ret = bot.AddTask("ruby-pipeline-task", ["set-ruby"])
  if ret == Robot::Ok
    ret = bot.AddTask("ruby-pipeline-task", ["check-ruby"])
  end
  if ret == Robot::Ok
    bot.Say("RUBY PIPELINE: queued")
    exit Robot::Normal
  end
  bot.Say("RUBY PIPELINE: add-task failed")
  exit Robot::Fail
when "set-ruby"
  if bot.SetParameter("PIPELINE_LANGUAGE", "ruby")
    bot.Say("RUBY PIPELINE TASK: set parameter ruby")
    exit Robot::Normal
  end
  bot.Say("RUBY PIPELINE TASK: set parameter failed")
  exit Robot::Fail
when "check-ruby"
  value = bot.GetParameter("PIPELINE_LANGUAGE")
  if value == "ruby"
    bot.Say("RUBY PIPELINE TASK: checked parameter ruby")
    exit Robot::Normal
  end
  bot.Say("RUBY PIPELINE TASK: checked parameter failed")
  exit Robot::Fail
end

exit 1
