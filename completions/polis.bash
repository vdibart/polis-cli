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
    local commands="about actor attest blessing branch clone comment did discover dm extract follow
        index init license mcp notifications post preview
        rebuild recover register render republish rotate-key serve site tag unfollow
        unpublish unregister validate version"

    # Subcommands for specific commands
    local blessing_subcommands="beseech deny grant requests sync"
    local dm_subcommands="list read send retry config decrypt publish-key"
    local attest_subcommands="issue list show verify withdraw register"
    local actor_subcommands="register withdraw list verify announce declare-operator"
    local branch_subcommands="deploy announce"
    local site_subcommands="set rewrite-unsigned"
    local notifications_subcommands="list clear"
    local tag_subcommands="list show apply remove delete"

    # Options for specific commands
    local notifications_list_opts="--type --json"
    local follow_opts="--announce --json"
    local unfollow_opts="--announce --json"
    local render_opts="--force --init-templates --json"
    local rebuild_opts="--posts --comments --tags --attestations --all --json"
    local init_opts="--site-title --register --posts-dir --comments-dir --keys-dir --snippets-dir --versions-dir --themes-dir --json"
    local unregister_opts="--force --json"
    local clone_opts="--full --diff --json"
    local discover_opts="--author --since --json"
    local rotate_key_opts="--json"
    local attest_issue_opts="--predicate --subject --subject-type --subject-version --payload --asserted --json"
    local branch_opts="--to --out --archive --from-domain --without-private --recovery-key --custody"
    local site_avatar_opts="--bg --fg --border --border-w --pattern --pattern-color --clear"
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
                dm)
                    if [[ $effective_pos -eq 1 ]]; then
                        COMPREPLY=($(compgen -W "$dm_subcommands --json" -- "$cur"))
                    fi
                    ;;
                tag)
                    if [[ $effective_pos -eq 1 ]]; then
                        COMPREPLY=($(compgen -W "$tag_subcommands --json" -- "$cur"))
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
                mcp)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "--site --help" -- "$cur"))
                    ;;
                attest)
                    if [[ $effective_pos -eq 1 ]]; then
                        COMPREPLY=($(compgen -W "$attest_subcommands --json" -- "$cur"))
                    elif [[ "${COMP_WORDS[$((cmd_pos + 1))]}" == "issue" && "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$attest_issue_opts" -- "$cur"))
                    fi
                    ;;
                actor)
                    [[ $effective_pos -eq 1 ]] && COMPREPLY=($(compgen -W "$actor_subcommands --json" -- "$cur"))
                    ;;
                branch)
                    if [[ $effective_pos -eq 1 && "$cur" != -* ]]; then
                        COMPREPLY=($(compgen -W "$branch_subcommands" -- "$cur"))
                    elif [[ "$cur" == -* ]]; then
                        case "${COMP_WORDS[$((cmd_pos + 1))]}" in
                            deploy) COMPREPLY=($(compgen -W "--config --help" -- "$cur")) ;;
                            announce) COMPREPLY=($(compgen -W "--help" -- "$cur")) ;;
                            *) COMPREPLY=($(compgen -W "$branch_opts --help" -- "$cur")) ;;
                        esac
                    fi
                    ;;
                recover)
                    if [[ $effective_pos -eq 1 ]]; then
                        COMPREPLY=($(compgen -W "sign" -- "$cur"))
                    elif [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "--domain --challenge --key" -- "$cur"))
                    fi
                    ;;
                site)
                    if [[ $effective_pos -eq 1 ]]; then
                        COMPREPLY=($(compgen -W "$site_subcommands" -- "$cur"))
                    elif [[ $effective_pos -eq 2 && "${COMP_WORDS[$((cmd_pos + 1))]}" == "set" ]]; then
                        COMPREPLY=($(compgen -W "author-name avatar" -- "$cur"))
                    elif [[ "${COMP_WORDS[$((cmd_pos + 2))]}" == "avatar" && "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "$site_avatar_opts" -- "$cur"))
                    fi
                    ;;
                license)
                    [[ $effective_pos -eq 1 ]] && COMPREPLY=($(compgen -W "reserved open --json" -- "$cur"))
                    ;;
                did)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "--host --write --json" -- "$cur"))
                    ;;
                serve)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$serve_opts" -- "$cur"))
                    ;;
                validate)
                    [[ "$cur" == -* ]] && COMPREPLY=($(compgen -W "$validate_opts" -- "$cur"))
                    ;;
                unpublish)
                    # Complete flags if typing flag, otherwise fall back to file completion
                    if [[ "$cur" == -* ]]; then
                        COMPREPLY=($(compgen -W "--json --yes -y" -- "$cur"))
                    fi
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
