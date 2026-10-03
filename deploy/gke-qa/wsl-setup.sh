#!/bin/sh
# One-time (per fresh WSL install) setup for running a real-GKE QA session
# from WSL. Both M10 and M12 hit the same gap independently -- a fresh
# native WSL environment has none of gcloud, the gke-gcloud-auth-plugin,
# sops or age, and the Windows-side installs of these (reached via
# /mnt/c/...) are either the wrong binary format (Windows .exe) or too
# slow to operate on over the 9p interop filesystem for gcloud's own
# component installer (see docs/plan/M10-gke-qa.md's e2e notes). This
# script installs a native, WSL-local copy of each, idempotently.
# Usage: deploy/gke-qa/wsl-setup.sh   (run inside WSL, not Git Bash)
set -eu

echo "# gcloud CLI (native WSL copy, not the Windows install under /mnt/c)"
if command -v gcloud >/dev/null 2>&1 && [ -x "$HOME/google-cloud-sdk/bin/gcloud" ]; then
  echo "ok   gcloud already installed at $HOME/google-cloud-sdk"
else
  curl -sL -o /tmp/gcloud-sdk.tar.gz \
    "https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-x86_64.tar.gz"
  tar xzf /tmp/gcloud-sdk.tar.gz -C "$HOME"
  rm /tmp/gcloud-sdk.tar.gz
  "$HOME/google-cloud-sdk/install.sh" --quiet --path-update=false --usage-reporting=false
  echo "ok   gcloud installed"
fi

export PATH="$HOME/google-cloud-sdk/bin:$PATH"
# Reuse the Windows-side login (gcloud auth login once there is enough;
# no need to log in twice) rather than prompting for a fresh browser flow.
export CLOUDSDK_CONFIG=/mnt/c/Users/PC/AppData/Roaming/gcloud

echo "# gke-gcloud-auth-plugin"
if command -v gke-gcloud-auth-plugin >/dev/null 2>&1; then
  echo "ok   gke-gcloud-auth-plugin already installed"
else
  yes | gcloud components install gke-gcloud-auth-plugin >/dev/null 2>&1
  echo "ok   gke-gcloud-auth-plugin installed"
fi

echo "# sops"
mkdir -p "$HOME/bin"
if [ -x "$HOME/bin/sops" ]; then
  echo "ok   sops already installed"
else
  curl -sL -o "$HOME/bin/sops" \
    "https://github.com/getsops/sops/releases/download/v3.9.4/sops-v3.9.4.linux.amd64"
  chmod +x "$HOME/bin/sops"
  echo "ok   sops installed"
fi

echo "# age (sops's local, non-GCP-KMS credential backend -- see D-058)"
if [ -x "$HOME/bin/age" ] && [ -x "$HOME/bin/age-keygen" ]; then
  echo "ok   age already installed"
else
  curl -sL -o /tmp/age.tar.gz \
    "https://github.com/FiloSottile/age/releases/download/v1.2.1/age-v1.2.1-linux-amd64.tar.gz"
  tar xzf /tmp/age.tar.gz -C /tmp
  mv /tmp/age/age /tmp/age/age-keygen "$HOME/bin/"
  rm -rf /tmp/age /tmp/age.tar.gz
  echo "ok   age installed"
fi

cat <<'EOF'

Done. For every QA session shell in WSL, export:
  export PATH="$HOME/bin:$HOME/google-cloud-sdk/bin:$PATH"
  export CLOUDSDK_CONFIG=/mnt/c/Users/PC/AppData/Roaming/gcloud
then gcloud container clusters get-credentials ... as usual.
EOF
