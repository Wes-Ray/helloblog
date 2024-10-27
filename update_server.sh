#!/bin/bash

# deploy necessary files to the remote server and restart blog service

set -e  # exit immediately if command exits with nonzero status

SCP_ARG="blog"
TARGET_DIR="/home/user/blog"
BIN_NAME="helloblog"
SERVICE_NAME="helloblog.service"


# compile go binary (delete after transfer)
echo "Building Go binary..."
if ! go build -o "$BIN_NAME" main.go 2> build_error.log; then
    echo "[!!ERROR!!]: Go build failed. Error message:"
    cat build_error.log
    rm build_error.log
    exit 1
fi
echo "Go build successful."
echo

transfer() {
    local item="$1"
    local path="$2"

    rsync -avz --progress "$item" "${SCP_ARG}:${path}"
}

items=(
    "dep"
    "images"
    "templates"
    "README.md"
    $BIN_NAME
)

for item in "${items[@]}"; do
    if [ -e "$item" ]; then
        echo "Transferring: $item"
        transfer "$item" "$TARGET_DIR"
    else
        echo "[!!WARNING!!]: $item does not exist, skipping."
    fi
done

rm "$BIN_NAME"

echo
echo "Transfer Complete"
echo

ssh -tt $SCP_ARG "bash -c 'sudo systemctl restart $SERVICE_NAME && \
            sudo systemctl is-active $SERVICE_NAME --quiet $SERVICE_NAME && \
            echo \"Blog service restart successful!\"' || \
            echo '[!!WARNING!!]: Blog service restart failed'"
