Add the Postman Insights agent to the Bookings API pod running on Kubernetes, send traffic through it, and show what
Insights discovers. Step 9 of demo-track.md. The pod runs in namespace gcho on the se-kube-demo EKS cluster (us-east-2);
read k8s/, .github/workflows/deploy-k8s.yml and teardown-k8s.yml, and the "Step 9" entry in demo-track.md first.

What Insights is (verified in Postman's docs on the day this was written)
- Postman Insights watches live API traffic and builds per-endpoint metrics and errors, and infers the endpoints.
  The agent processes what it sees on the host; raw traffic never leaves the host, only the analyzed results go to
  the Postman cloud over HTTPS (TLS 1.2). Authentication is a Postman API key whose owner can edit the Insights
  workspace. Raw request data is deleted after 30 days.
- Needs a Postman Enterprise plan, an INTERNAL workspace (not partner or public), a Workspace Admin or Editor, and
  Linux. It works for first-party REST APIs on Kubernetes, ECS, EC2 and Elastic Beanstalk.
- On Kubernetes the agent runs as a DaemonSet or as a sidecar. A sidecar covers one deployment only.
- Two ways to bind a sidecar to Postman. Project mode: `--project svc_xxxxxxxxxx` (an Insights project made in the app).
  WORKSPACE MODE (the one to use here): `--workspace-id <workspace id> --system-env <System Environment id>`, no Insights
  project at all. The traffic is then tied to the workspace and to an API Catalog System Environment (a team-wide label
  such as Dev or Demo). The reference demo (postman-solutions-eng/PostFinanceCo-Demo-Instructions, and
  SDLC-Demo-AWS-Shared-Host infra/lib/se-stack.ts) runs its sidecar this way and says only the Kubernetes agent commands
  support it. Its container: image public.ecr.aws/postman/postman-insights-agent:latest, args
  `apidump --workspace-id <ws> --system-env <env> --repro-mode`, env POSTMAN_INSIGHTS_API_KEY from a Secret plus the
  downward-API variables POSTMAN_K8S_NODE, POSTMAN_K8S_NAMESPACE, POSTMAN_K8S_POD, POSTMAN_K8S_HOST_IP,
  POSTMAN_K8S_POD_IP, requests and limits of 200m CPU and 500Mi memory, and securityContext capabilities add NET_RAW.
  Treat that as a reference, not a spec: check it against what `kube inject` produces.
- The reference demo's prerequisites (relevant here): a personal API key, not a service account, whose owner is a team
  Admin or Super Admin and whose team is the one being onboarded; a TEAM workspace that has been pushed to the Postman
  cloud at least once from the Postman DESKTOP APP (Files > Open folder, then Push to Postman Cloud; the script calls
  it desktop-app-only); the workspace's environments linked to System Environments in API Catalog.
- Docs: learning.postman.com/docs/insights/ (overview, get-started/kubernetes/sidecar, reference/agent/kube-inject,
  data/access, data/repro-mode-security, troubleshoot/traffic) and learning.postman.com/docs/api-catalog/connect/insights.
  Re-check the sidecar and kube-inject pages before you start; they are the source of truth, not this file.

Not verified (settle it by running and reading, then say so in your report)
- What the injected container looks like: its image, args, securityContext, capabilities and resource needs. The
  sidecar docs do not list them. One docs page says the agent uses no elevated privileges, but the shared DaemonSet
  manifest running on this cluster adds the capabilities NET_RAW, SYS_ADMIN and SYS_PTRACE and mounts the host's /proc,
  /var/run/netns and containerd socket. A sidecar needs less than a node-wide agent, but expect it to need at least
  NET_RAW (unverified). Read the output of `kube inject` and the agent's logs.
- The key name inside the Secret that `kube inject` creates for a sidecar, and how the agent reads it.
- Whether `kube inject` accepts `--workspace-id` and `--system-env` (check `--help`). If it does not, build the
  container by hand from the reference above and say so.
- Whether the workspace has ever been pushed to the Postman cloud from the desktop app (it was bootstrapped by the CLI).
  If traffic reaches the agent but nothing appears, this is the first thing to ask the presenter about.
- How to remove the sidecar (the docs do not cover it).
- What the app's tabs show (Overview, Errors, Latency, Diagnostics). The Postman MCP does not expose those screens,
  but it does expose the cloud data behind part of them (see "Check the cloud" under Verify). Use the API for what it
  can give you, ask the presenter only for the rest, and do not claim a screen's content without evidence.
- Whether a Workspace Mode sidecar shows up in the API Catalog tools at all, and under which name. The services seen
  there so far came from a DaemonSet in Discovery Mode. If nothing appears, say so; it does not by itself mean the agent
  failed.

Facts found in the cloud and on the cluster when this was written (re-check them; they were true on 2026-11-16)
- The team has 88 services discovered by Insights, from many people's work. None is for gcho/bookings-api, by name search
  through the Postman MCP, and the presenter confirmed it is not in the Postman app's Service Discovery screen either.
- A shared Insights agent runs on se-kube-demo as a DaemonSet: namespace postman-insights-namespace-fs-v12, name
  postman-insights-agent, created 2026-09-02, 6 of 6 pods running, arguments `kube run --repro-mode --discovery-mode`, no
  `--include-namespaces`. It is not ours. It covers every namespace including gcho, and Repro Mode is ON there, so it
  uploads the headers and payloads of 4xx and 5xx responses (kept up to 30 days).
- That agent already monitors our pod (its log shows `TrafficMonitoringRunning true` for the bookings-api pod) and logged
  `Registered discovered service "gcho/bookings-api" (ID svc_1Esi8lptSWfmPlUDgxy3UR)` at 2026-11-15T06:44:52Z, and again at
  08:04:52Z. Yet the service is not visible in the catalog tools or in the app. The cause is unknown. The same agent fails
  to register other teams' pods with `status code: 412`, and logs `Failed to send telemetry: Postman Insights
  verification token is empty`. No authentication or permission errors were seen.
- Not causes: a missing Insights project and a private workspace. Discovery Mode needs neither, and the agent registered
  the service regardless.
- By the docs a discovered service has a 24-hour capture window and capture pauses unless the service is onboarded in the
  app. For our service that window ends about 2026-11-16T06:44Z.
- Another DaemonSet, namespace rankesh-upi-payment-gateway, has 0 of 6 pods ready. It is also not ours.
- The presenter is Admin of the workspace "[gcho demo] bookings api" (id 9bedc149-084e-42b7-9328-00da323328b7; a private
  team workspace with two members, no tags) and of the team (Enterprise). The Insights upload quota meter shows about
  1.0 billion used against a 20 million limit; whether that blocks new data is unknown. Creating an Insights project
  was blocked until another project was deleted (a project-count cap), which Workspace Mode does not need.
- The System Environment chosen for this demo is DEMO (id 019e88ba-d6a7-7449-9467-3d7597391014, not production). It is
  shared with other people's services; the presenter chose it knowingly.
- The workspace "[gcho demo] bookings api" has four active monitors (QA Integration Tests, Reset Mock, [Contract] and
  [Smoke] Bookings API). Find out what URL each targets and how often it runs. A monitor aimed at the public URL adds
  traffic to Insights, and the Reset Mock monitor would reset the demo's state while you test.

Rules (this is a shared cluster and a shared Postman team)
- Sidecar only, in this pod. Do NOT install the DaemonSet: it listens to the traffic of every pod on a node, which on
  se-kube-demo means other teams' workloads, and it needs cluster-wide permissions that this namespace's roles do not have.
- The Postman API key is a secret. Never print it, put it in chat, a file in the repo, a manifest, a log, a workflow
  or a commit. Supply it only through an environment variable read with `read -s`, or standard input, and remove any
  scratch file that held it.
- Ask the presenter before each of these: creating anything in Postman (a System Environment, an environment
  association, an API key), anything that writes to the cluster, and committing or pushing. Those Postman objects are
  made in the app by the presenter.
- Download the agent's install script to a scratch directory outside the repo and read it before running it. Do not
  pipe curl into bash.
- Use `kubectl --context arn:aws:eks:us-east-2:005904641462:cluster/se-kube-demo` explicitly; never change the current
  context. Read other namespaces' objects only to answer a question, change nothing there, and never read another team's
  Secret (the shared agent's API key is one).
- Deployments go through the deploy-k8s workflow, not a local `kubectl apply`. A local `kubectl` is for reading
  (get, logs, describe) and for creating the one Secret described below.

What the presenter provides
- Confirmation that the workspace "[gcho demo] bookings api" was pushed to the Postman cloud from the desktop app, and
  that its environment for the public URL is linked to the System Environment DEMO in API Catalog (ask; the
  `getApiCatalogSystemEnvironmentAssociations` tool can show the association read-only).
- A PERSONAL Postman API key (not a service account, not the CI key) owned by a team Admin of the team that owns the
  workspace. Before use, confirm the key's team with the Postman API `/me` call (read the team name only, never print
  the key). Repro Mode (below) is the presenter's decision as well.
- No Insights project is needed in Workspace Mode. The IDs used are the workspace id and the DEMO System Environment id
  above, both identifiers, not credentials.

Steps
1. Read-only checks: `kubectl config current-context` is se-kube-demo; `kubectl -n gcho get deployment,pod,service`
   shows one running pod; the public URL answers /healthz. Stop and report if not. Then look at what already exists:
   - Cloud: `getApiCatalogDiscoveryServices` (source insights_project, search "bookings") should return nothing for
     gcho/bookings-api yet. If it returns something, report it before changing anything.
   - Cluster: `kubectl get daemonset -A` and look for a Postman Insights agent. On 2026-11-16 the answer was: a shared one
     covers gcho and monitors our pod, but its registration of our service is not visible to us (see "Facts found").
     The default is therefore the SIDECAR, which depends only on our own key and project. Do not touch the shared agent.
     Per the docs a DaemonSet skips pods that already carry the sidecar, so ours would not be captured twice, and the
     shared agent (Repro Mode on) would stop uploading our error payloads to an account we do not control.
     Re-check: if the situation has changed (for example the shared agent's service for gcho is now visible in the
     catalog), STOP and ask the presenter whether to rely on that agent or to add the sidecar anyway. Relying on it needs
     no new container, but the capture belongs to someone else's agent and key: you cannot set its filters or rate limit,
     its data goes to the project tied to its key, and its owner can change or remove it.
2. Install the CLI `postman-insights-agent` (see the sidecar page for the install script). Confirm
   `postman-insights-agent kube inject --help` lists the flags you use.
3. Inject into the BASE manifest, not the rendered one, so kustomize keeps working: run `kube inject` with
   `--workspace-id 9bedc149-084e-42b7-9328-00da323328b7 --system-env 019e88ba-d6a7-7449-9467-3d7597391014`
   (Workspace Mode; fall back to the reference container by hand if the flags do not exist) on k8s/deployment.yaml, write the result to a scratch file with `-o`, and the Secret to a separate
   scratch file with `-s="<path>"` (that needs `-o`). Pass the key as POSTMAN_API_KEY in the environment. Diff the
   output against k8s/deployment.yaml and copy only what is new (the agent container and its settings) into
   k8s/deployment.yaml by hand, keeping its comments, the image placeholder `bookings-api`, and every existing field.
4. Fit the sidecar into this pod:
   - Resources: the app asks for 50m CPU and 32Mi. Give the agent its own requests and a memory limit that is
     reasonable for a demo and say how you chose them; watch its real use with `kubectl top` if metrics exist.
   - Security: the pod runs as non-root 65532 with RuntimeDefault seccomp, and the app container drops all
     capabilities and has a read-only root filesystem. The pod-level settings also apply to the sidecar. If the agent
     does not start or cannot capture under them, do NOT loosen the pod. Report its logs and propose the narrowest
     change for the sidecar container only, and wait for the presenter's decision. The shared DaemonSet's manifest adds
     NET_RAW, SYS_ADMIN and SYS_PTRACE, and the reference sidecar adds only NET_RAW: propose exactly NET_RAW,
     for the sidecar container only, and only if the logs show it is needed. Never add SYS_ADMIN, SYS_PTRACE, host
     mounts or privileged mode without the presenter's explicit approval.
   - Filters: Kubernetes probes call /healthz constantly and would dominate the sampled traffic. Add
     `--path-exclusions '^/healthz$'` (a Go regular expression, anchored). The mock-only route /v1/__mock/reset is not
     in the spec; leave it unfiltered unless told otherwise, because it shows up as an endpoint the spec does not have,
     which is the point of the drift talk.
   - Repro Mode: do not add `--repro-mode` unless the presenter says so. With it on, the headers and payloads of 4xx
     and 5xx responses are uploaded and kept up to 30 days and are visible to anyone with Viewer access to the project.
     Postman redacts credentials and known keywords such as x-api-key, but not every sensitive value.
5. Create the Secret in the namespace once, from the scratch Secret file produced in step 3 (`kubectl -n gcho apply -f`),
   after the presenter agrees. Then delete that scratch file. Do not put the Secret in k8s/ or the kustomization:
   kustomize and deploy-k8s would otherwise commit or manage the key.
6. Render and check: `kubectl kustomize k8s/` still produces the Service and a Deployment with TWO containers, the
   app image still gets the tag from kustomization.yaml, and no API key appears anywhere in the output.
7. Make teardown remove the Secret: kustomize does not know it, so `kubectl delete -k k8s/` leaves it behind. Add
   `kubectl -n gcho delete secret postman-agent-secrets --ignore-not-found` (use the real Secret name you found)
   to the deletion step of .github/workflows/teardown-k8s.yml, and update its header comments.
8. Hand over: the presenter commits and runs deploy-k8s with an image_tag. Do not commit or run workflows yourself.

Verify (report the exact commands and what you saw; say plainly what you could not check)
- `kubectl -n gcho get pods` shows 2/2 ready; `kubectl -n gcho logs <pod> -c postman-insights-agent` shows the agent
  running and no errors; the app container is unchanged.
- The shared DaemonSet agent should stop monitoring the new pod, because it now carries a sidecar (docs claim; verify it).
  Find the agent pod on the same node as our pod (`kubectl -n gcho get pod -o wide`, then
  `kubectl -n postman-insights-namespace-fs-v12 get pod -o wide`), read its recent log
  (`kubectl logs --tail=400 <agent pod>`) and look for the new bookings-api pod name: it should not appear as
  `TrafficMonitoringRunning`. Report what you saw. Read-only; do not change that namespace.
- Generate traffic against the public URL (http://<load balancer host>/v1, key demo-key) for at least 10 minutes:
  run "Reset Mock", then [Smoke] with `-i resources -i bookings -i webhook-subscriptions`, the QA Lifecycle folder and
  the QA data-driven folder with `-d qa/qa-create-booking.csv`, repeated several times with `--iteration-count`.
  Those produce 2xx, 401, 404, 409 and 422 responses. Use `--no-report-events`. There are no 429 or 500 responses, and
  there is no webhook delivery, so those will not appear.
- Endpoints take 5 to 8 minutes after the first traffic. Check the cloud yourself first (next bullet), then ask the
  presenter to confirm only what the API cannot show: the project's Diagnostics tab lists the agent; the Overview lists
  the endpoints you exercised; the Errors tab shows the 4xx ones; the Latency tab shows percentiles; /healthz is absent.
- Check the cloud (Postman MCP, read-only):
  - `getApiCatalogDiscoveryServices` with source insights_project and search "bookings": the service name is
    `<namespace>/<workload>`, so expect `gcho/bookings-api`. Report its status (discovered or integrated), its
    endpointsCount and its providerMetadata (cluster, namespace, workload, discovery mode).
  - With Workspace Mode the service should carry the DEMO System Environment already. If it is in the API Catalog, call
    `getApiCatalogSystemEnvironments`, then `getApiCatalogServices` (by name and systemEnvironmentId) to get the catalogued
    service id, then `getApiCatalogServiceEndpoints` (serviceId and systemEnvironmentId; sort by count, errorRate or
    p95LatencyMs; filter by responseCodes). That gives per-endpoint request counts, status codes, p95 latency and error
    rate. Expect the spec's operations you exercised, 2xx plus 401, 404, 409 and 422, and no /healthz.
  - A discovered service and a catalogued service have DIFFERENT ids (the endpoints call returns 404 if you mix them),
    and the metrics cover a recent window (the last 7 days by default).
  - Other people's services appear in the same lists. Read only what you need and do not report them.
- If only health checks or nothing shows after 10 minutes: check the Diagnostics tab and the agent logs, then the
  path exclusions and the sampling and rate limit (the agent samples traffic; the default is 1000 calls per minute).
- API Catalog: if the service is not yet there, the presenter can bring it in by hand (API Catalog > Service Discovery >
  Postman Insights Catalog). Do not create system environments or catalog entries yourself.

Report
- Verified / Not verified for every item above, what the injected container actually contained, any security change
  you proposed and whether it was approved, and what the presenter still has to do.
- To undo: revert the manifest change and redeploy, then delete the Secret. The teardown-k8s workflow does the latter
  once step 7 is in.
