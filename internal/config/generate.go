package config

// DefaultConfigTOML is a complete, commented sample kahi.toml.
const DefaultConfigTOML = `# Kahi configuration file
# See https://github.com/kahiteam/kahi for documentation.

[supervisor]
# logfile = ""                  # daemon log file path (default: stdout)
# log_level = "info"            # debug, info, warn, error
# log_format = "json"           # json, text
# directory = ""                # daemon working directory
# identifier = "kahi"           # daemon identifier
# minfds = 1024                 # minimum file descriptors
# minprocs = 200                # minimum process count
# nocleanup = false             # preserve stale log files on startup
# shutdown_timeout = 30         # seconds to wait for graceful shutdown

[server.unix]
# file = "/var/run/kahi.sock"   # Unix socket path
# chmod = "0700"                # socket file permissions
# chown = ""                    # socket owner (user:group)

[server.http]
# enabled = false               # enable TCP HTTP server
# listen = "127.0.0.1:9876"    # TCP listen address
# username = ""                 # HTTP Basic Auth username
# password = ""                 # bcrypt-hashed password

# Process definitions
# [programs.example]
# command = "/usr/bin/example"  # REQUIRED: command to run
# process_name = "example"     # name template (supports %(process_num)d)
# numprocs = 1                 # number of instances
# numprocs_start = 0           # starting instance number
# priority = 999               # start order (0=first, 999=last)
# autostart = true             # start on daemon startup
# autorestart = "unexpected"   # true, false, unexpected
# startsecs = 1                # seconds before considered started
# startretries = 3             # max retries before FATAL
# exitcodes = [0]              # expected exit codes
# stopsignal = "TERM"          # stop signal (TERM, HUP, INT, QUIT, KILL, USR1, USR2)
# stopwaitsecs = 10            # seconds to wait before SIGKILL
# stopasgroup = false          # send stop signal to process group
# killasgroup = false          # send SIGKILL to process group
# user = ""                    # run as user
# directory = ""               # working directory
# umask = ""                   # file creation mask
# clean_environment = false    # whitelist-only environment mode
# redirect_stderr = false      # merge stderr into stdout
# strip_ansi = false           # remove ANSI escape sequences
# stdout_logfile = ""          # stdout log file (default: container stdout)
# stdout_logfile_maxbytes = "50MB"
# stdout_logfile_backups = 10
# stderr_logfile = ""          # stderr log file
# stderr_logfile_maxbytes = "50MB"
# stderr_logfile_backups = 10
# description = ""             # process description
# [programs.example.environment]
# KEY = "value"

# Group definitions
# [groups.services]
# programs = ["web", "api"]    # group member programs
# priority = 999               # group priority

# Webhook definitions
# [webhooks.slack]
# url = "https://hooks.slack.com/..."
# events = ["process_state"]
# timeout = 5
# retries = 3
# [webhooks.slack.headers]
# Authorization = "Bearer token"
`
