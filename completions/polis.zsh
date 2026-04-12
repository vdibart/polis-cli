#compdef polis
# Zsh completion for polis CLI
# Add to your fpath or source in ~/.zshrc:
#   fpath=(/path/to/polis/completions $fpath)
#   autoload -Uz compinit && compinit
#
# Or copy to ~/.zsh/completions/_polis (create dir if needed)

_polis() {
    local -a commands blessing_subcommands dm_subcommands notifications_subcommands tag_subcommands
    local cmd_pos=2  # Default command position

    commands=(
        'about:Show site, versions, config, keys, discovery info'
        'blessing:Manage comment blessings'
        'clone:Clone a remote polis site (--full, --diff)'
        'comment:Create a comment on a post (--filename, --title for stdin)'
        'discover:Check followed authors for new content (--author, --since)'
        'dm:Direct messages (list, read, send, retry, config)'
        'extract:Reconstruct a specific version of a file'
        'follow:Follow an author (--announce to broadcast)'
        'index:View content index'
        'init:Initialize Polis directory structure'
        'notifications:View and manage notifications'
        'post:Create a new post (--filename, --title for stdin)'
        'preview:Preview a post or comment with signature verification'
        'rebuild:Rebuild indexes (--posts, --comments, --notifications, --all)'
        'register:Register site with discovery service'
        'render:Render markdown to HTML (--force, --init-templates)'
        'republish:Update an already-published file'
        'rotate-key:Generate new keypair and re-sign content (--delete-old-key)'
        'serve:Start local web server (bundled binary only, -d/--data-dir)'
        'tag:Manage content tags (list, show, apply, remove, delete)'
        'unfollow:Unfollow an author (--announce to broadcast)'
        'unpublish:Remove a published post and notify discovery service'
        'unregister:Unregister site from discovery service (--force to skip confirmation)'
        'validate:Validate site structure (--json)'
        'version:Print CLI version'
    )

    blessing_subcommands=(
        'beseech:Re-request blessing by content hash'
        'deny:Deny a blessing request'
        'grant:Grant a blessing request'
        'requests:List pending blessing requests'
        'sync:Sync auto-blessed comments from discovery service'
    )

    dm_subcommands=(
        'list:List DM conversations'
        'read:Read messages in a conversation'
        'send:Send a DM to a recipient'
        'retry:Retry delivering unsent messages'
        'config:Show DM acceptance policy'
    )

    notifications_subcommands=(
        'list:List notifications (--type to filter)'
    )

    tag_subcommands=(
        'list:List all tags'
        'show:Show targets for a tag'
        'apply:Tag content with a tag name'
        'remove:Remove a target from a tag'
        'delete:Delete an entire tag'
    )

    # Adjust command position if --json is first
    if [[ "$words[2]" == "--json" ]]; then
        cmd_pos=3
    fi

    local actual_cmd="$words[$cmd_pos]"

    _arguments -C \
        '--json[Output results in JSON format]' \
        '--help[Show help]' \
        '1: :->command' \
        '*: :->args'

    case $state in
        command)
            _describe -t commands 'polis commands' commands
            ;;
        args)
            # If we're completing right after --json, show commands
            if [[ "$words[2]" == "--json" && $CURRENT -eq 3 ]]; then
                _describe -t commands 'polis commands' commands
                return
            fi

            case $actual_cmd in
                blessing)
                    if [[ $CURRENT -eq $((cmd_pos + 1)) ]]; then
                        _describe -t subcommands 'blessing subcommands' blessing_subcommands
                    fi
                    ;;
                dm)
                    if [[ $CURRENT -eq $((cmd_pos + 1)) ]]; then
                        _describe -t subcommands 'dm subcommands' dm_subcommands
                    else
                        local subcmd="$words[$((cmd_pos + 1))]"
                        case $subcmd in
                            read)
                                _arguments ':conversation_id:'
                                ;;
                            send)
                                _arguments ':recipient_url:' ':message:'
                                ;;
                            retry)
                                _arguments ':conversation_id:'
                                ;;
                        esac
                    fi
                    ;;
                tag)
                    if [[ $CURRENT -eq $((cmd_pos + 1)) ]]; then
                        _describe -t subcommands 'tag subcommands' tag_subcommands
                    else
                        local subcmd="$words[$((cmd_pos + 1))]"
                        case $subcmd in
                            show|delete)
                                _arguments ':tag_name:'
                                ;;
                            apply|remove)
                                _arguments ':tag_name:' ':target_uri:'
                                ;;
                        esac
                    fi
                    ;;
                notifications)
                    if [[ $CURRENT -eq $((cmd_pos + 1)) ]]; then
                        _describe -t subcommands 'notifications subcommands' notifications_subcommands
                    else
                        local subcmd="$words[$((cmd_pos + 1))]"
                        case $subcmd in
                            list)
                                _arguments \
                                    '--json[Output in JSON format]' \
                                    '--type[Filter by notification type]:type:(version_available version_pending new_follower new_post blessing_changed)'
                                ;;
                        esac
                    fi
                    ;;
                follow)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--announce[Broadcast follow to discovery service]' \
                        ':url:'
                    ;;
                unfollow)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--announce[Broadcast unfollow to discovery service]' \
                        ':url:'
                    ;;
                render)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--force[Force re-render all files]' \
                        '--init-templates[Create default templates in .polis/templates]'
                    ;;
                rebuild)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--posts[Rebuild public.jsonl from posts and comments]' \
                        '--comments[Rebuild blessed-comments.json]' \
                        '--notifications[Reset notification files]' \
                        '--all[Rebuild all indexes and reset notifications]'
                    ;;
                unregister)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--force[Skip confirmation prompt]'
                    ;;
                init)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--site-title[Set custom site title]:title:' \
                        '--register[Auto-register with discovery service]' \
                        '--posts-dir[Custom posts directory]:directory:_files -/' \
                        '--comments-dir[Custom comments directory]:directory:_files -/' \
                        '--keys-dir[Custom keys directory]:directory:_files -/' \
                        '--snippets-dir[Custom snippets directory]:directory:_files -/' \
                        '--versions-dir[Custom versions directory]:directory:_files -/' \
                        '--themes-dir[Custom themes directory]:directory:_files -/'
                    ;;
                clone)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--full[Re-download all content]' \
                        '--diff[Only download new/changed content]' \
                        ':url:'
                    ;;
                discover)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--author[Check a specific author]:url:' \
                        '--since[Show items since date]:date:'
                    ;;
                rotate-key)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--delete-old-key[Delete old keypair instead of archiving]'
                    ;;
                post)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--filename[Output filename for stdin mode]:filename:' \
                        '--title[Override title extraction]:title:' \
                        ':file:_files'
                    ;;
                comment)
                    _arguments \
                        '--json[Output in JSON format]' \
                        '--filename[Output filename for stdin mode]:filename:' \
                        '--title[Override title extraction]:title:' \
                        ':url:' \
                        ':file:_files'
                    ;;
                republish)
                    _arguments \
                        '--json[Output in JSON format]' \
                        ':file:_files'
                    ;;
                preview)
                    _arguments \
                        '--json[Output in JSON format]' \
                        ':url:'
                    ;;
                extract)
                    _arguments \
                        '--json[Output in JSON format]' \
                        ':file:_files' \
                        ':version-hash:'
                    ;;
                serve)
                    _arguments \
                        '-d[Polis site directory]:directory:_files -/' \
                        '--data-dir[Polis site directory]:directory:_files -/'
                    ;;
                validate)
                    _arguments '--json[Output in JSON format]'
                    ;;
                unpublish)
                    _arguments \
                        '--json[Output in JSON format]' \
                        ':file:_files'
                    ;;
                about|version|index|register)
                    _arguments '--json[Output in JSON format]'
                    ;;
            esac
            ;;
    esac
}

_polis "$@"
