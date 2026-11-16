#!/usr/bin/env bash
# Records real tofu output for an encrypted state, for the fake tofu in tests to replay. It
# encrypts testdata/tofu/src/plain.tfstate.json with a pbkdf2 key from TF_ENCRYPTION in a local
# backend, then records the encrypted file, tofu show -json, and show with a wrong key. Every AWS
# credential source is blanked, so nothing can reach AWS; init downloads the aws provider from
# the registry. Run it again after a tofu upgrade and review the diff; encrypted.tfstate differs
# on every run because each encryption uses a new salt and nonce.
#   bash scripts/record-tofu.sh
set -euo pipefail

version=${TOFU_VERSION:-1.13.9}
src=testdata/tofu/src
out=testdata/tofu/secure
work=$(cd "$(mktemp -d)" && pwd -P)
trap 'rm -rf "$work"' EXIT
cp "$src/main.tf" "$work/main.tf"

keys='key_provider "pbkdf2" "main" { passphrase = "%s" }
method "aes_gcm" "main" { keys = key_provider.pbkdf2.main }
method "unencrypted" "migrate" {}
state {
  method = method.aes_gcm.main
  %s
}'
# shellcheck disable=SC2059 # the format string is the HCL template above
migrate=$(printf "$keys" FIXTURE-SECRET-passphrase 'fallback { method = method.unencrypted.migrate }')
# shellcheck disable=SC2059
right=$(printf "$keys" FIXTURE-SECRET-passphrase '')
# shellcheck disable=SC2059
wrong=$(printf "$keys" FIXTURE-SECRET-wrong-passphrase '')

tofu() {
  (cd "$work" && env -u AWS_PROFILE -u AWS_DEFAULT_PROFILE -u AWS_ACCESS_KEY_ID \
    -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
    AWS_CONFIG_FILE=/dev/null AWS_SHARED_CREDENTIALS_FILE=/dev/null AWS_EC2_METADATA_DISABLED=true \
    TF_IN_AUTOMATION=1 TF_INPUT=0 mise exec "opentofu@$version" -- tofu "$@")
}

tofu init -input=false -no-color >/dev/null
# State must carry the schema versions of the provider that reads it.
tofu providers schema -json >"$work/schema.json"
jq --slurpfile s "$work/schema.json" '
  ($s[0].provider_schemas["registry.opentofu.org/hashicorp/aws"]) as $p
  | .resources |= map(.mode as $m | .type as $t
      | (if $m == "data" then $p.data_source_schemas[$t].version else $p.resource_schemas[$t].version end) as $v
      | .instances |= map(.schema_version = ($v // 0)))' \
  "$src/plain.tfstate.json" >"$work/terraform.tfstate"

# Removing a resource makes tofu write the state again, encrypted with the primary method.
TF_ENCRYPTION=$migrate tofu state rm -no-color aws_ssm_parameter.drop >/dev/null

rm -rf "$out"
mkdir -p "$out"
{
  tofu version | head -1
  grep -E '^\s*version' "$work/.terraform.lock.hcl" | head -1 | sed -E 's/.*"(.*)"/hashicorp\/aws \1/'
} >"$out/VERSION"
cp "$work/terraform.tfstate" "$out/encrypted.tfstate"
TF_ENCRYPTION=$right tofu show -json -no-color >"$out/show.json"
set +e
TF_ENCRYPTION=$wrong tofu show -json -no-color >/dev/null 2>"$out/show-wrong-key.stderr"
echo $? >"$out/show-wrong-key.exit"
set -e
