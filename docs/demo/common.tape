# Width and Height are set per tape, before sourcing this file.
# Shared settings, sourced by every tape. Starts each recording on a clean
# prompt with a fresh update-check cache (hidden).
Set Shell bash
Set FontSize 15
Set Padding 18
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set TypingSpeed 45ms
Set WaitPattern /\$\s*$/
Set WaitTimeout 3m

Hide
Type "export PS1='\[\e[1;32m\]server\[\e[0m\]:\[\e[1;34m\]~\[\e[0m\]$ ' && dockup check >/dev/null; clear"
Enter
Wait
Show
