package config

// DefaultConfigTOML is a complete, commented sample kahi.toml.
const DefaultConfigTOML = `# Kahi configuration file
# See https://github.com/kahiteam/kahi for documentation.

[supervisor]
# logfile = ""                  # daemon log file path (default: stdout)
# log_level = "info"            # debug, info, warn, error
# log_format = "json"           # json, text
# directory = ""                # daemon working directory, applied at startup (default: inherited)
# identifier = "kahi"           # daemon identifier (not implemented yet)
# minfds = 1024                 # raise the NOFILE soft limit to at least this at startup
# minprocs = 200                # raise the NPROC soft limit to at least this at startup
# umask = ""                    # daemon umask, e.g. "0027" (default: inherited)
# user = ""                    # drop to this user after binding (name or uid, optionally :group);
#                               # needs root; kahi daemon -u takes precedence
# nocleanup = false             # no effect: kahi does not auto-name child logs
# shutdown_timeout = 30         # total seconds for an ordered shutdown, then SIGKILL; levels stop one after
#                               # another, so keep it >= the sum of the largest stopwaitsecs in each level,
#                               # and below your service manager's stop timeout (TimeoutStopSec, docker stop -t)

[server.unix]
# file = "/var/run/kahi.sock"   # Unix socket path. Default: this as root; else
#                               # $XDG_RUNTIME_DIR/kahi.sock or <tmp>/kahi-<uid>/kahi.sock
# chmod = "0700"                # socket permissions; must be owner-only -- group/other access is rejected

[server.http]
# enabled = false               # enable TCP HTTP server (requires username + password below)
# listen = "127.0.0.1:9876"    # TCP listen address; loopback is NOT exempt from auth
# username = ""                 # HTTP Basic Auth username (required when enabled)
# password = ""                 # bcrypt hash from "kahi hash-password" -- plaintext is rejected

# [server.web]                  # Web UI under /ui/ (requires [server.http])
# enabled = false               # serve the Web UI
# static_dir = ""               # optional per-file overlay for /ui/static/

# Process definitions
# [programs.example]
# command = "/usr/bin/example"  # REQUIRED: command to run
# process_name = "example"     # name template (supports %(process_num)d)
# numprocs = 1                 # number of instances
# numprocs_start = 0           # starting instance number
# priority = 999               # start order (0=first, 999=last); shutdown runs in reverse, and programs with
#                               # the same priority stop together, so sharing one shortens shutdown
# autostart = true             # start on daemon startup
# autorestart = "unexpected"   # true, false, unexpected
# startsecs = 1                # seconds before considered started
# startretries = 3             # max retries before FATAL
# exitcodes = [0]              # expected exit codes
# stopsignal = "SIGTERM"       # stop signal (SIGTERM, SIGHUP, SIGINT, SIGQUIT, SIGKILL, SIGUSR1, SIGUSR2)
# stopwaitsecs = 10            # seconds to wait before SIGKILL; a slow stop holds up the next priority level
# stopasgroup = false          # send stop signal to process group
# killasgroup = false          # send SIGKILL to process group
# user = ""                    # name or uid, optionally :group; needs a root daemon
# directory = ""               # working directory
# umask = ""                   # file creation mask
# clean_environment = false    # pass only supervisor + explicit env vars (no inheritance)
# inherit_environment = false  # for a process whose user differs from the supervisor, opt back
#                              # into full env inheritance (differing-user children are clean by default)
# redirect_stderr = false      # merge stderr into stdout
# strip_ansi = false           # remove ANSI escape sequences
# stdout_events_enabled = false # publish PROCESS_LOG_STDOUT events (events API, listeners)
# stderr_events_enabled = false # publish PROCESS_LOG_STDERR events
# stdout_logfile = ""          # stdout log file (default: container stdout)
# stdout_logfile_maxbytes = "50MB"
# stdout_logfile_backups = 10
# stderr_logfile = ""          # stderr log file
# stderr_logfile_maxbytes = "50MB"
# stderr_logfile_backups = 10
# stdout_capture_maxbytes = "64KB" # in-memory buffer for tail and the log viewer, 1KB..16MB
# stderr_capture_maxbytes = "64KB"
# description = ""             # process description
# [programs.example.environment]
# KEY = "value"

# Group definitions
# [groups.services]
# programs = ["web", "api"]    # group member programs
# priority = 999               # group priority

# Webhook notifications: an HTTP POST per listed event (not PROCESS_LOG_*)
# [webhooks.slack]
# url = "${SLACK_WEBHOOK_URL}"  # https; plain http only for localhost or with allow_insecure
# events = ["PROCESS_STATE_FATAL"]
# template = "slack"            # generic (default), slack, pagerduty
# timeout = 5                   # seconds per POST
# max_retries = 3               # retries after the first attempt (5xx, 408, 429, network errors)
# allow_insecure = false        # accept plain http:// to a non-loopback host
# routing_key = ""              # PagerDuty integration key; required with template = "pagerduty"
# [webhooks.slack.headers]
# Authorization = "Bearer ${API_TOKEN}"
`
