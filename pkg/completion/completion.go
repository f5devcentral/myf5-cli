// Package completion provides shell autocompletion script generators for myf5.
// Supported shells: Bash, Zsh, and Fish.
package completion

import (
	"fmt"
	"io"
)

// SupportedShells returns the list of shells supported for completion script generation.
func SupportedShells() []string {
	return []string{"bash", "zsh", "fish"}
}

// Generate writes the completion script for the specified shell to w.
func Generate(shell string, w io.Writer) error {
	switch shell {
	case "bash":
		return GenBashCompletion(w)
	case "zsh":
		return GenZshCompletion(w)
	case "fish":
		return GenFishCompletion(w)
	default:
		return fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish)", shell)
	}
}

// GenBashCompletion outputs a Bash completion script to w.
func GenBashCompletion(w io.Writer) error {
	script := `# bash completion for myf5                                 -*- shell-script -*-

_myf5_completions() {
    local cur prev words cword
    _init_completion -n : || return

    local commands="downloads cases ihealth bigip bigiq push doctor completion version help"
    local downloads_actions="browse families lines versions containers files get"
    local cases_actions="list metadata comment"
    local ihealth_actions="list upload"
    local bigip_actions="ready qkview push download"
    local bigiq_actions="ready push"
    local completion_shells="bash zsh fish"
    local product_families="BIG-IP NGINX BIG-IQ"

    if [[ $cword -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
        return 0
    fi

    case "${words[1]}" in
        downloads|download)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=( $(compgen -W "$downloads_actions" -- "$cur") )
                return 0
            fi
            case "${words[2]}" in
                lines|versions|containers|files|get)
                    if [[ $cword -eq 3 ]]; then
                        COMPREPLY=( $(compgen -W "$product_families" -- "$cur") )
                        return 0
                    fi
                    ;;
            esac
            if [[ "$cur" == -* ]]; then
                COMPREPLY=( $(compgen -W "--profile -o --output --push --push-method --push-dest --push-user --push-port --push-key --push-target" -- "$cur") )
                return 0
            fi
            ;;
        cases|case)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=( $(compgen -W "$cases_actions" -- "$cur") )
                return 0
            fi
            ;;
        ihealth)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=( $(compgen -W "$ihealth_actions" -- "$cur") )
                return 0
            fi
            if [[ "${words[2]}" == "upload" && $cword -eq 3 ]]; then
                _filedir '@(qkview|tar.gz|tgz)'
                return 0
            fi
            ;;
        bigip)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=( $(compgen -W "$bigip_actions" -- "$cur") )
                return 0
            fi
            if [[ "$cur" == -* ]]; then
                COMPREPLY=( $(compgen -W "-u --user -p --port -m --method -d --dest -k --key" -- "$cur") )
                return 0
            fi
            ;;
        bigiq)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=( $(compgen -W "$bigiq_actions" -- "$cur") )
                return 0
            fi
            if [[ "$cur" == -* ]]; then
                COMPREPLY=( $(compgen -W "-u --user -p --port -m --method -d --dest -k --key" -- "$cur") )
                return 0
            fi
            ;;
        push)
            if [[ "$cur" == -* ]]; then
                COMPREPLY=( $(compgen -W "-m --method -d --dest -t --target -u --user -p --port -k --key" -- "$cur") )
                return 0
            fi
            ;;
        completion)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=( $(compgen -W "$completion_shells" -- "$cur") )
                return 0
            fi
            ;;
    esac
}

complete -F _myf5_completions myf5
`
	_, err := io.WriteString(w, script)
	return err
}

// GenZshCompletion outputs a Zsh completion script to w.
func GenZshCompletion(w io.Writer) error {
	script := `#compdef myf5

_myf5() {
    local -a commands
    commands=(
        'downloads:Browse MyF5 catalog and stream software images'
        'cases:View and manage MyF5 support cases'
        'ihealth:Upload and inspect QKView diagnostic bundles on iHealth'
        'bigip:Manage BIG-IP TMOS appliances (ready check, QKView generation, push)'
        'bigiq:Manage BIG-IQ appliances (ready check, push images/files)'
        'push:Push software downloads or files to BIG-IP / BIG-IQ via iControl or SCP'
        'doctor:Verify API credentials, DNS, and network firewall egress'
        'completion:Generate shell tab completion script (bash, zsh, fish)'
        'version:Print application version'
        'help:Show help information'
    )

    _arguments -C \
        '1: :->command' \
        '*:: :->args'

    case $state in
        command)
            _describe -t commands 'myf5 command' commands
            ;;
        args)
            case $words[1] in
                downloads|download)
                    local -a download_actions
                    download_actions=(
                        'browse:Interactive menu-driven image browser'
                        'families:List all F5 product families'
                        'lines:List product lines for a family'
                        'versions:List releases, containers, and dates'
                        'containers:List categories/containers for a version'
                        'files:List downloadable files with sizes'
                        'get:Download software image with live progress'
                    )
                    _arguments -C \
                        '(-o --output)'{-o,--output}'[Destination file path]:file:_files' \
                        '--profile[Credentials profile]:profile:(default)' \
                        '--push[Appliance host to push to after download]:host:_hosts' \
                        '--push-method[Transfer method]:method:(icontrol scp)' \
                        '--push-dest[Destination directory on appliance]:dest:' \
                        '--push-user[Appliance username]:user:' \
                        '--push-target[Appliance type]:target:(BIG-IP BIG-IQ)' \
                        '1: :->d_action' \
                        '*:: :->d_args'

                    case $state in
                        d_action)
                            _describe -t download_actions 'downloads action' download_actions
                            ;;
                        d_args)
                            case $words[1] in
                                lines|versions|containers|files|get)
                                    if (( CURRENT == 2 )); then
                                        local -a families
                                        families=('BIG-IP' 'NGINX' 'BIG-IQ')
                                        _describe -t families 'product family' families
                                    fi
                                    ;;
                            esac
                            ;;
                    esac
                    ;;
                cases|case)
                    local -a case_actions
                    case_actions=(
                        'list:List active support cases'
                        'metadata:Retrieve support case schema metadata'
                        'comment:Add a note to an existing case'
                    )
                    _arguments -C \
                        '1: :->c_action' \
                        '*:: :->c_args'

                    case $state in
                        c_action)
                            _describe -t case_actions 'cases action' case_actions
                            ;;
                    esac
                    ;;
                ihealth)
                    local -a ihealth_actions
                    ihealth_actions=(
                        'list:List recent QKView uploads on iHealth'
                        'upload:Upload a QKView bundle to iHealth'
                    )
                    _arguments -C \
                        '1: :->ih_action' \
                        '*:: :->ih_args'

                    case $state in
                        ih_action)
                            _describe -t ihealth_actions 'ihealth action' ihealth_actions
                            ;;
                        ih_args)
                            if [[ $words[1] == "upload" && $CURRENT -eq 2 ]]; then
                                _files -g '*.qkview'
                            fi
                            ;;
                    esac
                    ;;
                bigip)
                    local -a bigip_actions
                    bigip_actions=(
                        'ready:Check TMOS management plane readiness'
                        'qkview:Generate and download a diagnostic QKView bundle'
                        'push:Upload software image or file via iControl REST or SCP'
                    )
                    _arguments -C \
                        '(-u --user)'{-u,--user}'[BIG-IP administrative user]:user:' \
                        '(-p --port)'{-p,--port}'[Management HTTPS / SSH port]:port:' \
                        '(-m --method)'{-m,--method}'[Transfer method]:method:(icontrol scp)' \
                        '(-d --dest)'{-d,--dest}'[Remote destination path]:dest:' \
                        '(-k --key)'{-k,--key}'[SSH private key path]:file:_files' \
                        '1: :->b_action' \
                        '2:host:_hosts'

                    case $state in
                        b_action)
                            _describe -t bigip_actions 'bigip action' bigip_actions
                            ;;
                    esac
                    ;;
                bigiq)
                    local -a bigiq_actions
                    bigiq_actions=(
                        'ready:Check BIG-IQ management plane readiness'
                        'push:Upload software image or file via iControl REST or SCP'
                    )
                    _arguments -C \
                        '(-u --user)'{-u,--user}'[BIG-IQ administrative user]:user:' \
                        '(-p --port)'{-p,--port}'[Management HTTPS / SSH port]:port:' \
                        '(-m --method)'{-m,--method}'[Transfer method]:method:(icontrol scp)' \
                        '(-d --dest)'{-d,--dest}'[Remote destination path]:dest:' \
                        '(-k --key)'{-k,--key}'[SSH private key path]:file:_files' \
                        '1: :->biq_action' \
                        '2:host:_hosts'

                    case $state in
                        biq_action)
                            _describe -t bigiq_actions 'bigiq action' bigiq_actions
                            ;;
                    esac
                    ;;
                push)
                    _arguments -C \
                        '(-m --method)'{-m,--method}'[Transfer method]:method:(icontrol scp)' \
                        '(-d --dest)'{-d,--dest}'[Remote destination directory]:dest:' \
                        '(-t --target)'{-t,--target}'[Target appliance type]:target:(BIG-IP BIG-IQ)' \
                        '(-u --user)'{-u,--user}'[Administrative user]:user:' \
                        '(-p --port)'{-p,--port}'[Port]:port:' \
                        '(-k --key)'{-k,--key}'[SSH private key path]:file:_files' \
                        '1:host:_hosts' \
                        '2:file:_files'
                    ;;
                completion)
                    local -a shells
                    shells=('bash:Generate Bash completion script' 'zsh:Generate Zsh completion script' 'fish:Generate Fish completion script')
                    _describe -t shells 'shell' shells
                    ;;
            esac
            ;;
    esac
}

_myf5 "$@"
`
	_, err := io.WriteString(w, script)
	return err
}

// GenFishCompletion outputs a Fish completion script to w.
func GenFishCompletion(w io.Writer) error {
	script := `# fish completion for myf5

# Disable file completions by default unless explicitly requested
complete -c myf5 -f

# Main commands
complete -c myf5 -n "__fish_use_subcommand" -a downloads -d "Browse MyF5 catalog and stream software images"
complete -c myf5 -n "__fish_use_subcommand" -a cases -d "View and manage MyF5 support cases"
complete -c myf5 -n "__fish_use_subcommand" -a ihealth -d "Upload and inspect QKView diagnostic bundles on iHealth"
complete -c myf5 -n "__fish_use_subcommand" -a bigip -d "Manage BIG-IP TMOS appliances"
complete -c myf5 -n "__fish_use_subcommand" -a bigiq -d "Manage BIG-IQ appliances"
complete -c myf5 -n "__fish_use_subcommand" -a push -d "Push downloads or files to BIG-IP/BIG-IQ"
complete -c myf5 -n "__fish_use_subcommand" -a doctor -d "Verify API credentials, DNS, and network reachability"
complete -c myf5 -n "__fish_use_subcommand" -a version -d "Print application version"
complete -c myf5 -n "__fish_use_subcommand" -a completion -d "Generate shell tab completion script"
complete -c myf5 -n "__fish_use_subcommand" -a help -d "Show help information"

# Downloads subcommands
complete -c myf5 -n "__fish_seen_subcommand_from downloads download; and not __fish_seen_subcommand_from browse families lines versions containers files get" -a browse -d "Interactive image browser"
complete -c myf5 -n "__fish_seen_subcommand_from downloads download; and not __fish_seen_subcommand_from browse families lines versions containers files get" -a families -d "List all product families"
complete -c myf5 -n "__fish_seen_subcommand_from downloads download; and not __fish_seen_subcommand_from browse families lines versions containers files get" -a lines -d "List product lines for family"
complete -c myf5 -n "__fish_seen_subcommand_from downloads download; and not __fish_seen_subcommand_from browse families lines versions containers files get" -a versions -d "List releases and containers"
complete -c myf5 -n "__fish_seen_subcommand_from downloads download; and not __fish_seen_subcommand_from browse families lines versions containers files get" -a containers -d "List containers/categories"
complete -c myf5 -n "__fish_seen_subcommand_from downloads download; and not __fish_seen_subcommand_from browse families lines versions containers files get" -a files -d "List downloadable files"
complete -c myf5 -n "__fish_seen_subcommand_from downloads download; and not __fish_seen_subcommand_from browse families lines versions containers files get" -a get -d "Download software image"
complete -c myf5 -n "__fish_seen_subcommand_from downloads download" -l profile -d "Credentials profile"
complete -c myf5 -n "__fish_seen_subcommand_from downloads download" -s o -l output -F -d "Destination file path"
complete -c myf5 -n "__fish_seen_subcommand_from downloads download" -l push -d "Push to host after download"

# Cases subcommands
complete -c myf5 -n "__fish_seen_subcommand_from cases case; and not __fish_seen_subcommand_from list metadata comment" -a list -d "List active support cases"
complete -c myf5 -n "__fish_seen_subcommand_from cases case; and not __fish_seen_subcommand_from list metadata comment" -a metadata -d "Get case schema metadata"
complete -c myf5 -n "__fish_seen_subcommand_from cases case; and not __fish_seen_subcommand_from list metadata comment" -a comment -d "Add comment to case"

# iHealth subcommands
complete -c myf5 -n "__fish_seen_subcommand_from ihealth; and not __fish_seen_subcommand_from list upload" -a list -d "List recent QKView uploads"
complete -c myf5 -n "__fish_seen_subcommand_from ihealth; and not __fish_seen_subcommand_from list upload" -a upload -d "Upload a QKView file"
complete -c myf5 -n "__fish_seen_subcommand_from ihealth; and __fish_seen_subcommand_from upload" -F

# BIG-IP subcommands
complete -c myf5 -n "__fish_seen_subcommand_from bigip; and not __fish_seen_subcommand_from ready qkview push" -a ready -d "Check TMOS readiness"
complete -c myf5 -n "__fish_seen_subcommand_from bigip; and not __fish_seen_subcommand_from ready qkview push" -a qkview -d "Generate diagnostic QKView"
complete -c myf5 -n "__fish_seen_subcommand_from bigip; and not __fish_seen_subcommand_from ready qkview push" -a push -d "Push file via iControl or SCP"
complete -c myf5 -n "__fish_seen_subcommand_from bigip" -s u -l user -d "BIG-IP admin username"
complete -c myf5 -n "__fish_seen_subcommand_from bigip" -s p -l port -d "Management HTTPS/SSH port"

# BIG-IQ subcommands
complete -c myf5 -n "__fish_seen_subcommand_from bigiq; and not __fish_seen_subcommand_from ready push" -a ready -d "Check BIG-IQ readiness"
complete -c myf5 -n "__fish_seen_subcommand_from bigiq; and not __fish_seen_subcommand_from ready push" -a push -d "Push file via iControl or SCP"
complete -c myf5 -n "__fish_seen_subcommand_from bigiq" -s u -l user -d "BIG-IQ admin username"
complete -c myf5 -n "__fish_seen_subcommand_from bigiq" -s p -l port -d "Management HTTPS/SSH port"

# Push command
complete -c myf5 -n "__fish_seen_subcommand_from push" -s m -l method -d "Transfer method (icontrol or scp)"
complete -c myf5 -n "__fish_seen_subcommand_from push" -s t -l target -d "Target appliance (BIG-IP or BIG-IQ)"
complete -c myf5 -n "__fish_seen_subcommand_from push" -s d -l dest -d "Remote destination directory"

# Completion shells
complete -c myf5 -n "__fish_seen_subcommand_from completion" -a "bash zsh fish"
`
	_, err := io.WriteString(w, script)
	return err
}
