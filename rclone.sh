#!/bin/sh
lockfile -r 0 $1/uploadscript.lock || exit 1
rclone sync --exclude="*.mosaic.*" onedrive:/lsm/ $1/
