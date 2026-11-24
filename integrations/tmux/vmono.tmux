# vmono's tmux integration, written by `vmono integration install tmux`.
#
# vmono rewrites this file from its own binary on every install, so it never
# drifts from the version you run. Put your own changes in your tmux.conf,
# after the line that sources this file; a later bind-key wins.

# prefix + V opens the vmono TUI in a popup. Launching a session from it
# creates or switches to that session's own tmux session (named pkg/session)
# and closes the popup. VMONO_LAUNCHER=tmux makes sure a harness is never
# started inside the popup itself, whatever the launcher setting says.
bind-key V display-popup -E -w 90% -h 85% -d '#{pane_current_path}' 'env VMONO_LAUNCHER=tmux vmono'
