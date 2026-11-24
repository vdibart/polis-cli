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
        'actor:The registry of system actors this operator runs'
        'attest:Signed claims about other people and works'
        'blessing:Manage comment blessings'
        'branch:Turn an export of your site into a new site at a new address'
        'clone:Clone a remote polis site (--full, --diff)'
        'comment:Create a comment on a post (--filename, --title for stdin)'
        'did:Show or write the site'"'"'s DID document (.well-known/did.json)'
        'discover:Check followed authors for new content (--author, --since)'
        'dm:Direct messages (list, read, send, retry, config, decrypt, publish-key)'
        'extract:Reconstruct a specific version of a file'
        'follow:Follow an author (--announce to broadcast)'
        'index:View content index'
        'init:Initialize Polis directory structure'
        'license:Show or state the terms your work may be used under'
        'notifications:View and manage notifications'
        'post:Create a new post (--filename, --title for stdin)'
        'preview:Preview a post or comment with signature verification'
        'rebuild:Rebuild the content index (--posts, --comments, --tags, --attestations, --all)'
        'recover:Sign a sign-in recovery challenge with your recovery key'
        'register:Register site with discovery service'
        'render:Render markdown to HTML (--force, --init-templates)'
        'republish:Update an already-published file'
        'rotate-key:Generate a new keypair and record the handover in the key history'
        'mcp:Serve MCP over stdio for an agent on this machine (token in POLIS_TOKEN)'
        'serve:Start local web server (bundled binary only, -d/--data-dir)'
        'site:Set the author name or avatar, or rewrite a signed file unsigned'
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
        'decrypt:Decrypt messages with your messages key'
        'publish-key:Publish (or create) your messages key'
    )

    notifications_subcommands=(
        'list:List notifications (--type to filter)'
        'clear:Delete local notification state'
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
                        '--posts[Rebuild the post entries in index.jsonl]' \
                        '--comments[Rebuild the comment entries in index.jsonl and reconcile blessed.json]' \
                        '--tags[Rebuild the tag entries in index.jsonl]' \
                        '--attestations[Rebuild the attestation entries in index.jsonl]' \
                        '--all[Rebuild every content type]'
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
                    _arguments '--json[Output in JSON format]'
                    ;;
                attest)
                    if [[ $CURRENT -eq $((cmd_pos + 1)) ]]; then
                        local -a attest_subcommands=(
                            'issue:Issue a new attestation' 'list:List attestations this site has issued'
                            'show:Show one attestation' 'verify:Verify issued attestations'
                            'withdraw:Retract a claim you issued' 'register:Announce a record to the discovery service')
                        _describe -t subcommands 'attest subcommands' attest_subcommands
                    elif [[ "$words[$((cmd_pos + 1))]" == issue ]]; then
                        _arguments \
                            '--predicate[What is being asserted, fully qualified]:predicate:' \
                            '--subject[What the claim is about (an https URL)]:url:' \
                            '--subject-type[identity or uri]:type:(identity uri)' \
                            '--subject-version[Pin a uri subject: sha256:<hex>]:hash:' \
                            '*--payload[Predicate-specific detail, k=v]:payload:' \
                            '--asserted[RFC 3339 time]:time:' \
                            '--json[Output in JSON format]'
                    fi
                    ;;
                actor)
                    if [[ $CURRENT -eq $((cmd_pos + 1)) ]]; then
                        local -a actor_subcommands=(
                            'register:Add or update an actor in the registry' 'withdraw:Remove an actor, permanently'
                            'list:Show the registry' 'verify:Check the registry or a signed action'
                            'announce:Announce a registered actor' 'declare-operator:Name the operator accountable for this actor')
                        _describe -t subcommands 'actor subcommands' actor_subcommands
                    fi
                    ;;
                branch)
                    if [[ $CURRENT -eq $((cmd_pos + 1)) && "$PREFIX" != -* ]]; then
                        local -a branch_subcommands=('deploy:Deploy a branched site' 'announce:Tell your discovery service where this site was branched from')
                        _describe -t subcommands 'branch subcommands' branch_subcommands
                        _files
                    else
                        case "$words[$((cmd_pos + 1))]" in
                            deploy) _arguments '--config[Deploy settings]:file:_files' ':branch-output:_files -/' ;;
                            announce) _arguments '--help[Show help]' ;;
                            *) _arguments \
                                '--to[The new address (https://…)]:url:' \
                                '--out[Directory for the new site]:directory:_files -/' \
                                '--archive[Write a zip archive instead]:file:_files' \
                                '--from-domain[The origin'"'"'s domain]:domain:' \
                                '--without-private[Reset the private residue to defaults]' \
                                '--recovery-key[Where to write the recovery key]:file:_files' \
                                '--custody[Which key you keep]:custody:(server)' \
                                ':export:_files' ;;
                        esac
                    fi
                    ;;
                recover)
                    if [[ $CURRENT -eq $((cmd_pos + 1)) ]]; then
                        local -a recover_subcommands=('sign:Sign a recovery challenge')
                        _describe -t subcommands 'recover subcommands' recover_subcommands
                    else
                        _arguments \
                            '--domain[The site you are signing in to]:host:' \
                            '--challenge[The challenge, as a file]:file:_files' \
                            '--key[Your recovery key]:file:_files'
                    fi
                    ;;
                site)
                    if [[ $CURRENT -eq $((cmd_pos + 1)) ]]; then
                        local -a site_subcommands=('set:Set the author name or avatar' 'rewrite-unsigned:Rewrite a signed file unsigned (never signs)')
                        _describe -t subcommands 'site subcommands' site_subcommands
                    elif [[ "$words[$((cmd_pos + 1))]" == set && $CURRENT -eq $((cmd_pos + 2)) ]]; then
                        local -a site_set=('author-name:The name shown on your site' 'avatar:Your mark'"'"'s colours and pattern')
                        _describe -t fields 'site set' site_set
                    elif [[ "$words[$((cmd_pos + 2))]" == avatar ]]; then
                        _arguments \
                            '--bg[Background colour]:colour:' '--fg[Foreground colour]:colour:' \
                            '--border[Border colour]:colour:' '--border-w[Border width, 0-3]:width:' \
                            '--pattern[Pattern]:pattern:(none rings cross grid dots stripes diamond halves)' \
                            '--pattern-color[Pattern colour]:colour:' '--clear[Remove the custom avatar]'
                    elif [[ "$words[$((cmd_pos + 1))]" == rewrite-unsigned ]]; then
                        _files
                    fi
                    ;;
                license)
                    if [[ $CURRENT -eq $((cmd_pos + 1)) ]]; then
                        local -a license_choices=('reserved:Read and quote with a link; no AI training' 'open:Anyone may use it for anything')
                        _describe -t choices 'terms' license_choices
                    fi
                    ;;
                did)
                    _arguments \
                        '--host[Canonical host]:host:' \
                        '--write[(Re)generate .well-known/did.json]' \
                        '--json[Output in JSON format]'
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
                mcp)
                    _arguments \
                        '--site[The site address]:address:' \
                        '--help[Show help]'
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
                        '--yes[Skip the confirmation prompt]' \
                        '-y[Skip the confirmation prompt]' \
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
