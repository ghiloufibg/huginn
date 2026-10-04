#!/bin/bash
# Builds a keyless "impersonated_service_account" Application Default
# Credentials file for a given service account, from the caller's own
# `gcloud auth application-default login` ADC. Closes D-060: the
# SA-key-creation org policy that blocked M10's `kms` environment only
# ever blocked *downloading a static key* -- ADC-based impersonation
# needs no key at all, just one interactive `gcloud auth
# application-default login` from a human (unavoidable: that's how ADC
# establishes its base identity), after which this script is fully
# non-interactive and reusable.
#
# Prerequisites:
#   - gcloud auth application-default login (once, by a human)
#   - the caller's account has roles/iam.serviceAccountTokenCreator on
#     the target service account (grant it once, see docs/plan/M10-gke-qa.md)
#
# Usage: deploy/gke-qa/build-impersonated-adc.sh <service-account-email> <output-file>
set -eu
SA=${1:?usage: build-impersonated-adc.sh <service-account-email> <output-file>}
OUT=${2:?usage: build-impersonated-adc.sh <service-account-email> <output-file>}

ADC_CONFIG_DIR=${CLOUDSDK_CONFIG:-$(gcloud info --format='value(config.paths.global_config_path)')}
MYADC="$ADC_CONFIG_DIR/application_default_credentials.json"
if [ ! -f "$MYADC" ]; then
  echo "error: no ADC found at $MYADC -- run 'gcloud auth application-default login' first" >&2
  exit 1
fi

# The file is pretty-printed ("key": "value", with a space after the
# colon), not compact JSON -- a naive '"key":"value"' grep pattern
# silently extracts nothing.
field() { grep -oE "\"$1\":[[:space:]]*\"[^\"]*\"" "$MYADC" | sed -E 's/.*"([^"]+)"$/\1/'; }
CID=$(field client_id)
CSEC=$(field client_secret)
RTOK=$(field refresh_token)
if [ -z "$CID" ] || [ -z "$CSEC" ] || [ -z "$RTOK" ]; then
  echo "error: could not extract client_id/client_secret/refresh_token from $MYADC" >&2
  exit 1
fi

cat > "$OUT" <<EOF
{
  "type": "impersonated_service_account",
  "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/$SA:generateAccessToken",
  "source_credentials": {
    "type": "authorized_user",
    "client_id": "$CID",
    "client_secret": "$CSEC",
    "refresh_token": "$RTOK"
  }
}
EOF
echo "wrote $OUT (impersonating $SA)"
echo "use as: GOOGLE_APPLICATION_CREDENTIALS=$OUT <command>"
