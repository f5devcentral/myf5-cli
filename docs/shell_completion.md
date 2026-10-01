# Shell Tab Autocompletion Guide

`myf5` includes built-in autocompletion generator support for **Zsh**, **Bash**, and **Fish** shells. Shell autocompletion provides tab completion for all commands, subcommands, flags, and file filters.

---

## Installation by Shell

### 1. Zsh (Default on macOS)

#### Current Terminal Session
To test autocompletion immediately in the current terminal:
```zsh
source <(myf5 completion zsh)
```

#### Persistent Setup (Recommended)
Add autocompletion to `~/.zshrc`:

```zsh
# Option A: Sourced dynamically in ~/.zshrc
echo 'source <(myf5 completion zsh)' >> ~/.zshrc
source ~/.zshrc

# Option B: Placed in your zsh completion functions directory
mkdir -p "${fpath[1]}"
myf5 completion zsh > "${fpath[1]}/_myf5"
```

---

### 2. Bash

#### Current Terminal Session
```bash
source <(myf5 completion bash)
```

#### Persistent Setup

##### Linux (Ubuntu / Debian / CentOS / RHEL)
Ensure the `bash-completion` package is installed, then save the completion script to the system completions directory:
```bash
myf5 completion bash | sudo tee /etc/bash_completion.d/myf5 > /dev/null
```

##### macOS (with Homebrew Bash)
```bash
myf5 completion bash > $(brew --prefix)/etc/bash_completion.d/myf5
```

##### User Profile (`~/.bashrc`)
```bash
echo 'source <(myf5 completion bash)' >> ~/.bashrc
source ~/.bashrc
```

---

### 3. Fish

#### Persistent Setup
Fish automatically loads completions placed in `~/.config/fish/completions/`:

```fish
mkdir -p ~/.config/fish/completions
myf5 completion fish > ~/.config/fish/completions/myf5.fish
```

---

## Autocompletion Features

| Scope | Autocompletion Behavior |
| :--- | :--- |
| **Primary Commands** | Tab completes `downloads`, `cases`, `ihealth`, `bigip`, `bigiq`, `push`, `doctor`, `completion`, `version`, `help` with contextual descriptions. |
| **Download Actions** | Tab completes `browse`, `families`, `lines`, `versions`, `containers`, `files`, `get`. |
| **Product Families** | Suggests common F5 product families: `BIG-IP`, `NGINX`, `BIG-IQ`. |
| **Case Actions** | Tab completes `list`, `metadata`, `comment`. |
| **iHealth Actions** | Tab completes `list`, `upload`. |
| **BIG-IP Actions** | Tab completes `ready`, `qkview`, `push`, `download`. |
| **BIG-IQ Actions** | Tab completes `ready`, `push`. |
| **File Filtering** | When running `myf5 ihealth upload <TAB>`, autocompletion automatically filters directory listings to `*.qkview`, `*.tar.gz`, `*.tgz` files. |
| **Appliance Push Flags** | Suggests `-m`, `--method` (`icontrol`, `scp`), `-d`, `--dest`, `-t`, `--target` (`BIG-IP`, `BIG-IQ`), `-u`, `--user`, `-p`, `--port`, `-k`, `--key`. |
| **Download Push Flags** | Suggests `--push`, `--push-method`, `--push-dest`, `--push-target`, `--push-user`, `--push-port`, `--push-key`. |
| **Global Flags** | Suggests `--profile`, `-o`, `--output`. |
