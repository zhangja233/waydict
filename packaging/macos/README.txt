Install: drag Waydict.app to the Applications symlink, then launch Waydict from Applications. Models are installed after launch and are not stored inside the application bundle.

Waydict asks for Microphone, Accessibility, and Input Monitoring access only when each feature needs it. Speech recognition stays local; model installation is the expected network operation.

CLI: optionally create a user-owned symlink with:
  mkdir -p "$HOME/.local/bin"
  ln -s /Applications/Waydict.app/Contents/MacOS/waydict "$HOME/.local/bin/waydict"

Uninstall: quit Waydict, remove Waydict.app, and remove its login item in System Settings if enabled. User data is under "$HOME/Library/Application Support/Waydict" and logs are under "$HOME/Library/Logs/Waydict".

Documentation: https://github.com/zhangja233/waydict
