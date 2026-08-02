#!/usr/bin/env bash
# Consume a completed request workflow artifact with a fail-closed trust boundary.
#
# Required env:
#   GH_TOKEN, REPOSITORY
#   SOURCE_RUN_ID, SOURCE_RUN_CONCLUSION, SOURCE_RUN_BRANCH
#   SOURCE_RUN_EVENT, SOURCE_RUN_NAME, SOURCE_RUN_REPOSITORY
#   EXPECTED_WORKFLOW_NAME, EXPECTED_ARTIFACT_NAME
#   REQUEST_KIND = preview-recovery | stable-recovery | stable-emergency
#
# Writes the validated request JSON to REQUEST_OUTPUT (default /tmp/request.json)
# and emits mode-specific GitHub Actions outputs when GITHUB_OUTPUT is set.
set -euo pipefail

repository="${REPOSITORY:?REPOSITORY is required}"
source_run_id="${SOURCE_RUN_ID:?SOURCE_RUN_ID is required}"
source_run_conclusion="${SOURCE_RUN_CONCLUSION:?SOURCE_RUN_CONCLUSION is required}"
source_run_branch="${SOURCE_RUN_BRANCH:?SOURCE_RUN_BRANCH is required}"
source_run_event="${SOURCE_RUN_EVENT:?SOURCE_RUN_EVENT is required}"
source_run_name="${SOURCE_RUN_NAME:?SOURCE_RUN_NAME is required}"
source_run_repository="${SOURCE_RUN_REPOSITORY:?SOURCE_RUN_REPOSITORY is required}"
expected_workflow_name="${EXPECTED_WORKFLOW_NAME:?EXPECTED_WORKFLOW_NAME is required}"
expected_artifact_name="${EXPECTED_ARTIFACT_NAME:?EXPECTED_ARTIFACT_NAME is required}"
request_kind="${REQUEST_KIND:?REQUEST_KIND is required}"
request_output="${REQUEST_OUTPUT:-/tmp/request.json}"
download_dir="${REQUEST_DOWNLOAD_DIR:-/tmp/release-request}"

if [ "$source_run_repository" != "$repository" ]; then
	echo "::error::request run repository is $source_run_repository, expected $repository" >&2
	exit 1
fi
if [ "$source_run_event" != "workflow_dispatch" ]; then
	echo "::error::request run event is $source_run_event, expected workflow_dispatch" >&2
	exit 1
fi
if [ "$source_run_branch" != "main-v2" ]; then
	echo "::error::request run branch is $source_run_branch, expected main-v2" >&2
	exit 1
fi
if [ "$source_run_conclusion" != "success" ]; then
	echo "::error::request run conclusion is $source_run_conclusion, expected success" >&2
	exit 1
fi
if [ "$source_run_name" != "$expected_workflow_name" ]; then
	echo "::error::request workflow is '$source_run_name', expected '$expected_workflow_name'" >&2
	exit 1
fi

mkdir -p "$download_dir"
# Confirm exactly one artifact with the expected name exists for this run.
artifact_count="$(
	gh api "repos/$repository/actions/runs/$source_run_id/artifacts" --paginate \
		--jq --arg name "$expected_artifact_name" \
		'[.artifacts[] | select(.name == $name and .expired == false)] | length'
)"
if [ "$artifact_count" != "1" ]; then
	echo "::error::expected exactly one live artifact named $expected_artifact_name, found $artifact_count" >&2
	exit 1
fi

rm -rf "$download_dir"
mkdir -p "$download_dir"
gh run download "$source_run_id" --repo "$repository" \
	--name "$expected_artifact_name" --dir "$download_dir"

if [ ! -f "$download_dir/request.json" ]; then
	echo "::error::request artifact is missing request.json" >&2
	exit 1
fi

case "$request_kind" in
preview-recovery)
	jq -e '
    type == "object" and
    (.tag | type == "string" and test("^v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)-preview\\.[1-9][0-9]*$")) and
    (keys | sort == ["tag"])
  ' "$download_dir/request.json" >/dev/null || {
		echo "::error::preview recovery request JSON is invalid" >&2
		exit 1
	}
	;;
stable-recovery)
	jq -e '
    type == "object" and
    (.tag | type == "string" and test("^v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$")) and
    (.publish_cli | type == "boolean") and
    (.publish_npm | type == "boolean") and
    (.publish_desktop | type == "boolean") and
    ((.publish_cli or .publish_npm or .publish_desktop) == true) and
    (keys | sort == ["publish_cli", "publish_desktop", "publish_npm", "tag"])
  ' "$download_dir/request.json" >/dev/null || {
		echo "::error::stable recovery request JSON is invalid" >&2
		exit 1
	}
	;;
stable-emergency)
	jq -e '
    type == "object" and
    (.reason | type == "string" and
      (. == "security" or . == "data-loss" or . == "update-blocker" or . == "service-unavailable")) and
    (keys | sort == ["reason"])
  ' "$download_dir/request.json" >/dev/null || {
		echo "::error::stable emergency request JSON is invalid" >&2
		exit 1
	}
	;;
*)
	echo "::error::unknown REQUEST_KIND: $request_kind" >&2
	exit 1
	;;
esac

cp "$download_dir/request.json" "$request_output"
echo "Validated $request_kind request artifact from run $source_run_id"
