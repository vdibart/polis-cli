# Bash completion for polis CLI
# Source this file in your ~/.bashrc:
#   source /path/to/polis/completions/polis.bash
#
# Or copy to ~/.local/share/bash-completion/completions/polis for auto-loading

_polis_completion() {
    local cur prev
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    # All top-level commands
    local commands="about blessing clone comment discover extract follow
        index init notifications post preview
        rebuild register render republish rotate-key serve unfollow
        unregister validate version"

    # Subcommands for specific commands
    local blessing_subcommands="beseech deny grant requests sync"
    local notifications_subcommands="list"

    # Options for specific commands
    local notifications_list_opts="--type --json"
    local follow_opts="--announce --json"
    local unfollow_opts="--announce --json"
    local render_opts="--force --init-templates --json"
    local rebuild_opts="--posts --comments --notifications --all --json"
    local init_opts="--site-title --register --posts-dir --comments-dir --keys-dir --snippets-dir --versions-dir --themes-dir --json"
    local unregister_opts="--force --json"
    local clone_opts="--full --diff --json"
    local discover_opts="--author --since --json"
    local rotate_key_opts="--delete-old-key --json"
    local post_opts="--filename --title --json"
    local comment_opts="--filename --title --json"
    local serve_opts="--data-dir -d"
    local validate_opts="--json"

    # Global options
    local global_opts="--json --help"

    # Determine actual command position (skip --json if it's first)
    local cmd_pos=1
    local actual_cmd=""
    if [[ "${COMP_WORDS[1]}" == "--json" ]]; then
        cmd_pos=2
    fi
    if [[ $COMP_CWORD -ge $cmd_pos ]]; then
        actual_cmd="${COMP_WORDS[$cmd_pos]}"
    fi

    # Calculate effective position relative to command
    local effective_pos=$((COMP_CWORD - cmd_pos))

    case $COMP_CWORD in
        1)
            # First argument: complete commands or global options
            COMPREPLY=($(compgen -W "$commands $global_opts" -- "$cur"))
            ;;
        *)
            # Handle --json as first argument
            if [[ "${COMP_WORDS[1]}" == "--json" && $COMP_CWORD -eq 2 ]]; then
                COMPREPLY=($(compgen -W "$commands" -- "$cur"))
                return
            fi

            # Handle command-specific completions
            case $actual_cmd in
                blessing)
                    if [[ $effective_pos -eq 1 ]]; then
                        COMPREPLY=($(compgen -W "$blessing_subcommands --json" -- "$cur"))
                    fi
                    ;;
                notifications)
                    if [[ $effective_pos -eq 1 ]]; then
                        COMPREPLY=($(compgen -W "$notifications_subcommands --json" -- "$cur"))
                    elif [[ $effective_pos -ge 2 ]]; then
                        local subcmd="${COMP_WORDS[$((cmd_pos + 1))]}"
                        case $subcmd in
                            list)
                                [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$notifications_list_opts" -- "$cur"))
                                ;;
                        esac
                    fi
                    ;;
                follow|unfollow)
                    # Only complete flags if typing a flag, otherwise allow default (URL input)
                    if [[ "$cur" == -* ]]; then
                        [[ "$actual_cmd" == "follow" ]] && COMPREPLY=($(compgen -W "$follow_opts" -- "$cur"))
                        [[ "$actual_cmd" == "unfollow" ]] && COMPREPLY=($(compgen -W "$unfollow_opts" -- "$cur"))
                    fi
                    ;;
                render)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$render_opts" -- "$cur"))
                    ;;
                rebuild)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$rebuild_opts" -- "$cur"))
                    ;;
                init)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$init_opts" -- "$cur"))
                    ;;
                unregister)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$unregister_opts" -- "$cur"))
                    ;;
                clone)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$clone_opts" -- "$cur"))
                    ;;
                discover)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$discover_opts" -- "$cur"))
                    ;;
                rotate-key)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$rotate_key_opts" -- "$cur"))
                    ;;
                post|republish)
                    # Complete flags if typing flag, otherwise fall back to file completion
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$post_opts" -- "$cur"))
                    fi
                    # Empty COMPREPLY + "-o default" = file completion
                    ;;
                comment)
                    # Complete flags if typing flag, otherwise fall back to file completion
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$comment_opts" -- "$cur"))
                    fi
                    ;;
                extract)
                    # First arg is file, second is hash - only complete flags
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "--json" -- "$cur"))
                    fi
                    ;;
                preview)
                    # These take URLs, not files - only complete flags
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "--json" -- "$cur"))
                    fi
                    ;;
                serve)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$serve_opts" -- "$cur"))
                    ;;
                validate)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$validate_opts" -- "$cur"))
                    ;;
                about|version|index|register)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "--json" -- "$cur"))
                    ;;
            esac
            ;;
    esac
}

# -o default: fall back to default completion (files) when COMPREPLY is empty
# -o bashdefault: fall back to bash default completions (variables, etc.)
complete -o default -o bashdefault -F _polis_completion polis
