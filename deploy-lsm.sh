#!/bin/bash
git pull --rebase
systemctl --user stop lsm
go build main.go structs.go
sudo mv main /usr/local/bin/lsm
systemctl --user start lsm
systemctl --user status lsm