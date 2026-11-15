#!/usr/bin/env bash
# Creates, shows and removes the AWS resources that belong to this repository's Kubernetes workflows
# (publish-k8s.yml, deploy-k8s.yml, teardown-k8s.yml). Run it yourself with ADMIN credentials, for example
# after `aws sso login`. No GitHub workflow runs it, and none can: the workflow roles have no IAM rights.
#
#   scripts/aws-resources.sh [status]                     read-only: what exists now. The default: no command = status
#   scripts/aws-resources.sh up        [--dry-run]        create what is missing, bring the rest back in line
#   scripts/aws-resources.sh down      [--dry-run] [--yes] [--delete-repository]
#
# The resources, all bound to this repo only (the trust policies match its numeric repository id, the main
# branch and one workflow file each):
#   - IAM role github-actions-bookings-api-publish    can only push images to the ECR repository
#   - IAM role github-actions-bookings-api-deploy     can describe the cluster, check an image, edit namespace gcho
#   - IAM role github-actions-bookings-api-teardown   can delete images in that repository, edit namespace gcho
#   - EKS access entries for deploy and teardown      AmazonEKSEditPolicy, limited to namespace gcho
#   - ECR repository gcho/bookings-api                immutable tags, scan on push
# `down` removes the access entries and the roles. It removes the ECR repository, and every image in it, only
# with --delete-repository.
#
# It never touches the GitHub OIDC identity provider (token.actions.githubusercontent.com): it is shared with other
# repositories' roles in the account. It also never touches the Kubernetes objects or the namespace; run the
# teardown-k8s workflow first (or `kubectl delete -k k8s/`) so no load balancer or image is left behind.
#
# After `down`, publish-k8s, deploy-k8s and teardown-k8s fail at "Sign in to AWS" until `up` is run again.
# `up` is safe to repeat: existing roles keep their identity and get their trust and permissions re-applied.
#
# Works with the macOS system bash (3.2). Needs the AWS CLI v2.
set -euo pipefail

# ---- settings (each can be overridden from the environment) ------------------------------------------------
EXPECTED_ACCOUNT="${EXPECTED_ACCOUNT:-005904641462}"   # refuses to run against any other account
AWS_REGION="${AWS_REGION:-us-east-2}"
CLUSTER_NAME="${CLUSTER_NAME:-se-kube-demo}"
NAMESPACE="${NAMESPACE:-gcho}"
ECR_REPOSITORY="${ECR_REPOSITORY:-gcho/bookings-api}"
GITHUB_SLUG="${GITHUB_SLUG:-ghcho20/dm-bookings-api}"
GITHUB_REPO_ID="${GITHUB_REPO_ID:-1453575080}"
GITHUB_OWNER_ID="${GITHUB_OWNER_ID:-22895160}"
ROLE_PREFIX="github-actions-bookings-api"
OIDC_HOST="token.actions.githubusercontent.com"
EDIT_POLICY_ARN="arn:aws:eks::aws:cluster-access-policy/AmazonEKSEditPolicy"

DRY_RUN=0
ASSUME_YES=0
DELETE_REPOSITORY=0

usage() {
  sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

die() { echo "error: $*" >&2; exit 2; }

# Runs a command that changes AWS, or only prints it with --dry-run.
mutate() {
  if [ "$DRY_RUN" = 1 ]; then
    echo "  [dry-run] aws $*"
  else
    aws "$@"
  fi
}

role_name()     { echo "${ROLE_PREFIX}-$1"; }
role_arn()      { echo "arn:aws:iam::${ACCOUNT_ID}:role/$(role_name "$1")"; }
workflow_for()  { echo "$1-k8s.yml"; }
repo_arn()      { echo "arn:aws:ecr:${AWS_REGION}:${ACCOUNT_ID}:repository/${ECR_REPOSITORY}"; }
cluster_arn()   { echo "arn:aws:eks:${AWS_REGION}:${ACCOUNT_ID}:cluster/${CLUSTER_NAME}"; }
has_entry()     { [ "$1" = deploy ] || [ "$1" = teardown ]; }

role_exists()  { aws iam get-role --role-name "$(role_name "$1")" >/dev/null 2>&1; }
entry_exists() { aws eks describe-access-entry --region "$AWS_REGION" --cluster-name "$CLUSTER_NAME" --principal-arn "$(role_arn "$1")" >/dev/null 2>&1; }
repo_exists()  { aws ecr describe-repositories --region "$AWS_REGION" --repository-names "$ECR_REPOSITORY" >/dev/null 2>&1; }

trust_policy() { # trust_policy <role>
  cat <<EOF
{"Version":"2012-10-17","Statement":[{"Effect":"Allow",
 "Principal":{"Federated":"arn:aws:iam::${ACCOUNT_ID}:oidc-provider/${OIDC_HOST}"},
 "Action":"sts:AssumeRoleWithWebIdentity",
 "Condition":{"StringEquals":{
  "${OIDC_HOST}:aud":"sts.amazonaws.com",
  "${OIDC_HOST}:repository_id":"${GITHUB_REPO_ID}",
  "${OIDC_HOST}:repository_owner_id":"${GITHUB_OWNER_ID}",
  "${OIDC_HOST}:ref":"refs/heads/main",
  "${OIDC_HOST}:job_workflow_ref":"${GITHUB_SLUG}/.github/workflows/$(workflow_for "$1")@refs/heads/main"}}}]}
EOF
}

policy_name() {
  case "$1" in
    publish)  echo "ecr-push-bookings-api" ;;
    deploy)   echo "eks-describe-and-image-check" ;;
    teardown) echo "teardown-bookings-api" ;;
  esac
}

permissions_policy() { # permissions_policy <role>
  case "$1" in
    publish) cat <<EOF
{"Version":"2012-10-17","Statement":[
 {"Sid":"EcrLogin","Effect":"Allow","Action":"ecr:GetAuthorizationToken","Resource":"*"},
 {"Sid":"PushOneRepo","Effect":"Allow","Action":["ecr:BatchCheckLayerAvailability","ecr:InitiateLayerUpload","ecr:UploadLayerPart","ecr:CompleteLayerUpload","ecr:PutImage","ecr:BatchGetImage","ecr:GetDownloadUrlForLayer","ecr:DescribeImages"],"Resource":"$(repo_arn)"}]}
EOF
    ;;
    deploy) cat <<EOF
{"Version":"2012-10-17","Statement":[
 {"Sid":"DescribeCluster","Effect":"Allow","Action":"eks:DescribeCluster","Resource":"$(cluster_arn)"},
 {"Sid":"CheckImage","Effect":"Allow","Action":"ecr:DescribeImages","Resource":"$(repo_arn)"}]}
EOF
    ;;
    teardown) cat <<EOF
{"Version":"2012-10-17","Statement":[
 {"Sid":"DeleteImagesInOneRepo","Effect":"Allow","Action":["ecr:ListImages","ecr:DescribeImages","ecr:BatchDeleteImage"],"Resource":"$(repo_arn)"},
 {"Sid":"DescribeCluster","Effect":"Allow","Action":"eks:DescribeCluster","Resource":"$(cluster_arn)"},
 {"Sid":"ConfirmLoadBalancerGone","Effect":"Allow","Action":["elasticloadbalancing:DescribeLoadBalancers","elasticloadbalancing:DescribeTags"],"Resource":"*"}]}
EOF
    ;;
  esac
}

role_description() {
  echo "GitHub Actions $(workflow_for "$1") (${GITHUB_SLUG}, main): $2"
}

role_purpose() {
  case "$1" in
    publish)  echo "push the bookings-api image to ECR" ;;
    deploy)   echo "deploy to namespace ${NAMESPACE} on ${CLUSTER_NAME}" ;;
    teardown) echo "remove the bookings-api deployment (namespace ${NAMESPACE}) and its ECR images" ;;
  esac
}

check_account() {
  command -v aws >/dev/null || die "the AWS CLI (aws) is required"
  ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)" || die "cannot read the AWS identity; run aws sso login first"
  if [ "$ACCOUNT_ID" != "$EXPECTED_ACCOUNT" ]; then
    die "signed in to account $ACCOUNT_ID, but this script is for $EXPECTED_ACCOUNT. Set EXPECTED_ACCOUNT to override."
  fi
}

# ---- status ------------------------------------------------------------------------------------------------
cmd_status() {
  echo "Account $ACCOUNT_ID, region $AWS_REGION, cluster $CLUSTER_NAME, namespace $NAMESPACE"
  if repo_exists; then
    n="$(aws ecr describe-images --region "$AWS_REGION" --repository-name "$ECR_REPOSITORY" --query 'length(imageDetails)' --output text 2>/dev/null || echo '?')"
    echo "  ECR repository $ECR_REPOSITORY: present ($n image(s))"
  else
    echo "  ECR repository $ECR_REPOSITORY: absent"
  fi
  for r in publish deploy teardown; do
    if role_exists "$r"; then echo "  IAM role $(role_name "$r"): present"; else echo "  IAM role $(role_name "$r"): absent"; fi
    if has_entry "$r"; then
      if entry_exists "$r"; then echo "  EKS access entry for $r: present"; else echo "  EKS access entry for $r: absent"; fi
    fi
  done
}

# ---- up ----------------------------------------------------------------------------------------------------
ensure_repository() {
  if repo_exists; then
    echo "  ECR repository $ECR_REPOSITORY: exists"
  else
    echo "  ECR repository $ECR_REPOSITORY: creating"
    mutate ecr create-repository --region "$AWS_REGION" --repository-name "$ECR_REPOSITORY" \
      --image-tag-mutability IMMUTABLE --image-scanning-configuration scanOnPush=true \
      --tags Key=owner,Value=gcho Key=project,Value=dm-bookings-api Key=purpose,Value=demo >/dev/null
  fi
}

ensure_role() { # ensure_role <role>
  local r="$1" name
  name="$(role_name "$r")"
  if role_exists "$r"; then
    echo "  IAM role $name: exists; re-applying trust and permissions"
    mutate iam update-assume-role-policy --role-name "$name" --policy-document "$(trust_policy "$r")"
  else
    echo "  IAM role $name: creating"
    mutate iam create-role --role-name "$name" --max-session-duration 3600 \
      --assume-role-policy-document "$(trust_policy "$r")" \
      --description "$(role_description "$r" "$(role_purpose "$r")")" \
      --tags Key=owner,Value=gcho Key=project,Value=dm-bookings-api Key=purpose,Value=demo >/dev/null
    [ "$DRY_RUN" = 1 ] || aws iam wait role-exists --role-name "$name"
  fi
  mutate iam put-role-policy --role-name "$name" --policy-name "$(policy_name "$r")" \
    --policy-document "$(permissions_policy "$r")"
}

ensure_entry() { # ensure_entry <role>
  local r="$1" arn attempt
  arn="$(role_arn "$r")"
  if entry_exists "$r"; then
    echo "  EKS access entry for $r: exists; re-applying the namespace-limited edit policy"
  else
    echo "  EKS access entry for $r: creating"
    if [ "$DRY_RUN" = 1 ]; then
      echo "  [dry-run] aws eks create-access-entry --cluster-name $CLUSTER_NAME --principal-arn $arn"
    else
      # A role created a moment ago may not be visible to EKS yet (IAM propagation): retry briefly.
      for attempt in 1 2 3 4 5 6; do
        if aws eks create-access-entry --region "$AWS_REGION" --cluster-name "$CLUSTER_NAME" --principal-arn "$arn" \
             --type STANDARD --tags owner=gcho,project=dm-bookings-api,purpose=demo >/dev/null 2>&1; then
          break
        fi
        [ "$attempt" = 6 ] && die "could not create the access entry for $arn"
        sleep 5
      done
    fi
  fi
  mutate eks associate-access-policy --region "$AWS_REGION" --cluster-name "$CLUSTER_NAME" --principal-arn "$arn" \
    --policy-arn "$EDIT_POLICY_ARN" --access-scope "type=namespace,namespaces=${NAMESPACE}" >/dev/null
}

cmd_up() {
  [ "$DRY_RUN" = 1 ] && echo "DRY RUN: nothing will be changed."
  echo "Creating or refreshing resources in account $ACCOUNT_ID:"
  ensure_repository
  for r in publish deploy teardown; do
    ensure_role "$r"
    if has_entry "$r"; then ensure_entry "$r"; fi
  done
  echo "Done. publish-k8s, deploy-k8s and teardown-k8s can sign in to AWS again."
}

# ---- down --------------------------------------------------------------------------------------------------
confirm_down() {
  [ "$DRY_RUN" = 1 ] && return 0
  [ "$ASSUME_YES" = 1 ] && return 0
  echo "This deletes, in account $ACCOUNT_ID: the two EKS access entries and the three IAM roles named ${ROLE_PREFIX}-*."
  [ "$DELETE_REPOSITORY" = 1 ] && echo "It also deletes the ECR repository $ECR_REPOSITORY and ALL images in it."
  echo "Run the teardown-k8s workflow first so no load balancer or image is left behind."
  printf 'Type delete to continue: '
  read -r answer
  [ "$answer" = "delete" ] || die "not confirmed; nothing was deleted"
}

remove_role() { # remove_role <role>
  local r="$1" name p
  name="$(role_name "$r")"
  if ! role_exists "$r"; then echo "  IAM role $name: already absent"; return 0; fi
  echo "  IAM role $name: deleting"
  for p in $(aws iam list-role-policies --role-name "$name" --query PolicyNames --output text); do
    mutate iam delete-role-policy --role-name "$name" --policy-name "$p"
  done
  mutate iam delete-role --role-name "$name"
}

cmd_down() {
  [ "$DRY_RUN" = 1 ] && echo "DRY RUN: nothing will be changed."
  confirm_down
  echo "Removing resources in account $ACCOUNT_ID:"
  for r in deploy teardown; do
    if entry_exists "$r"; then
      echo "  EKS access entry for $r: deleting"
      mutate eks delete-access-entry --region "$AWS_REGION" --cluster-name "$CLUSTER_NAME" --principal-arn "$(role_arn "$r")"
    else
      echo "  EKS access entry for $r: already absent"
    fi
  done
  for r in publish deploy teardown; do remove_role "$r"; done
  if [ "$DELETE_REPOSITORY" = 1 ]; then
    if repo_exists; then
      echo "  ECR repository $ECR_REPOSITORY: deleting with its images"
      mutate ecr delete-repository --region "$AWS_REGION" --repository-name "$ECR_REPOSITORY" --force >/dev/null
    else
      echo "  ECR repository $ECR_REPOSITORY: already absent"
    fi
  else
    echo "  ECR repository $ECR_REPOSITORY: kept (use --delete-repository to remove it)"
  fi
  echo "Done. The GitHub OIDC provider and the namespace were left alone."
}

# ---- main --------------------------------------------------------------------------------------------------
# With no command (or only options) the script runs `status`, which only reads. up and down must be asked for by name.
COMMAND=status
if [ $# -ge 1 ]; then
  case "$1" in
    -*) ;;                         # an option such as --help: keep the default command
    *)  COMMAND="$1"; shift ;;
  esac
fi
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    --yes) ASSUME_YES=1 ;;
    --delete-repository) DELETE_REPOSITORY=1 ;;
    -h|--help) usage 0 ;;
    *) die "unknown option: $arg" ;;
  esac
done

case "$COMMAND" in
  status) check_account; cmd_status ;;
  up)     check_account; cmd_up ;;
  down)   check_account; cmd_down ;;
  -h|--help|help) usage 0 ;;
  *) usage 2 ;;
esac
