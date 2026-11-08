# Bookings API Demo Track

## step 00: setup

#### Git:
- create a new repo
- add a `POSTMAN_SERVICE_ACCOUNT_API_KEY` key to Action secrets

#### PM WS:
- create a new `private` WS
- invite the `github-actions` service account holding the API key(`gcho-github-action-key`) for Action
- go to Teams settings
- assign `github-action` service account the new WS as Edit
- set up git-backed folder

## step 01: design

#### `postman spec lint <spec file>`
check the issues and fix
- issue: copy&paste blocks - common issues caused by coding agents

#### Policy tour (Enterprise value)
- WS > home > API Catalog > Governance Groups
- Ruleset lib
- Governed Groups
  - All workspaces (locked) - `Default-ruleset`
  - mention some other gov groups like `FinTech`, `SNAP`, ...

Custom rule set
- apply `401`, `403` rule set
- run `spec lint` again
- let your agent fix the problems

> Point out the value of shift-left - how important the early detection is in time & effort

## step 02: Bootstrap, Mock, Fern

#### WS-push (github action)
- explain why it's important for drift prevention: `postman-publish.yml`
- only pushed by Git iif postman part(`postman/`) is changed

#### Working with Postman App
##### collection generation via Agent Mode for prototyping
  - collection tour
    - collectioin level auth, env
    - some requests
##### mock generation inside PM
  - create > generate with AI
  - create a local `env`
  - run collection via mock
##### document via Agent Mode and adding detailed context to fields
  - switch to cloud
  - show 2 publishing options: fern vs postman
  
##### `Reset local repo`

#### Bootstrap (github action)
- stress the **purpose** of intentional uni-di sync (`syncSpecToCollection` only)
- the importance of **determinism**

#### Mock via Coding Agent
- show the prompt as a reference
- create a local `env`
- verify test run with the mock
  - don't forget to reset mock before Smoke test

#### Doc with PM
- Agent Mode: workspace > collection > folder > request > params

#### Fern Doc
- switch to cloud ws to show 2 options - fern & postman
  > tell the difference
- publish only `baseline collection` to Fern for documentation  
<br></br>
- connect to Git > go to `Github`
  - check `.fern/`, `fern/` folders
  - check `FERN_TOKEN`
- fetch(sync) the remote repo
- show `fern` folder in sync
- explain how bi-directional works

#### Fern SDK
- tweak `generators.yaml` to use the spec in github and generate sdks
  - local, git, package
- github action to generate & publish SDKs

## step 03: collaboration
comments & postman docs & invite users

#### Fern Doc
**Admin**
- open gunho.cho@postman.com session in Chrome
- go to Fern dashboard
- Members & invite ghcho20@gmail.com as `Editor`

#### QA
- invite another user in Postman
- add comment

## step 04: Fern Doc

#### Editor
- go to mailbox
- accept invite
- edit & `commit`
- `1. mark as ready for review`

#### Reviewer
- go to Github
- `merge PR`
- go to `Actions`: check `fern_check`, `preview-docs`, and `publish-docs`
<br></br>

- go to SDK repo/folder
- prove no SDK update by showing `1 commit`

## step 05: QA
show agent prompt for QA tests generation
- Contract, Smoke collection

## step 06: Go backend app
show agent prompt for Go app generation

## step 07: CI gate for the app
`ci-gate.yml`

## ---- no need for replay ----

## step 08: k8s deploy & Monitor

#### k8s setup
- `scripts/aws-resources.sh up`
- `publish-k8s.yml`: build pod & register ECR
- `deploy-k8s.yml`: deploy the service to k8s cluster

#### k8s teardown
- `teardown-k8s.yml`
- `scripts/aws-resources.sh down --delete-repository`

#### Monitor
- one time run > **collection** run
- hosted and scheduled run > **Monitor**
- work on cloud

## step 09: Insights & API Catalog
- `samples/agt_prompt/step-09-insights.md`: prompt for enabling a sidecar agent
<br></br>
> Known issue: the running Daemon Set agent can't register discovered services with 412 error