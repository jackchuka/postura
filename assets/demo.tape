# Regenerate with: vhs assets/demo.tape (from the repository root)
# Runs fully offline: `postura eval` on fabricated facts in assets/demo/.

Output assets/demo.gif

Set Shell "bash"
Set FontSize 18
Set Width 1440
Set Height 1060
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms

Hide
Type `export D=/tmp/postura-demo && rm -rf "$D" && mkdir -p "$D" && go build -o "$D/postura" . && cp assets/demo/rules.yaml assets/demo/config.yaml assets/demo/facts.json "$D" && cd "$D" && export PATH="$D:$PATH" PS1='\[\e[1;35m\]$ \[\e[0m\]' && clear`
Enter
Sleep 5s
Show

Type "postura rules list --rules rules.yaml"
Sleep 400ms
Enter
Sleep 2s

Type "cat config.yaml"
Sleep 400ms
Enter
Sleep 3s

Type "clear"
Enter
Type "postura eval --rules rules.yaml --config config.yaml --fail-on error facts.json; echo exit=$?"
Sleep 400ms
Enter
Sleep 6s
