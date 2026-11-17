# Set up the Postman Insights agent as a sidecar on a Kubernetes service

A walk-through for adding the Postman Insights agent as a **sidecar container** to one Kubernetes Deployment, so that
service's HTTP traffic builds an Insights project (and, through it, API Catalog entries). For the cluster-wide alternative
see `step-09-insights-daemonset-setup.md`.

**What this is based on.**
- Postman's docs: [Install Postman Insights Agent as a sidecar][docs-sidecar], the [`kube inject` reference][docs-inject]
  and [Get started with Postman Insights][docs-start].
- Eight sidecar-injected workloads I read (read-only) on the `se-kube-demo` EKS cluster on 2026-11-17. The docs do not
  document the container that `kube inject` adds, so that part comes from those live deployments.
- I tried Postman's own docs search through the Postman MCP first. It timed out, so I read the documentation pages
  directly.

I did **not** inject a sidecar into anything while writing this, so the steps are not freshly tested end to end. Where the
docs and the live cluster don't say something, this guide says so instead of guessing.

[docs-sidecar]: https://learning.postman.com/docs/insights/get-started/kubernetes/sidecar/
[docs-inject]: https://learning.postman.com/docs/insights/reference/agent/kube-inject/
[docs-start]: https://learning.postman.com/docs/insights/get-started/overview

## The picture

The numbers match the steps below.

```
  [1] POSTMAN                                         [2] POSTMAN
      Insights project                                    API key
      (Add icon > Insights > name > Setup tab)            ("Add API key" on the onboarding screen)
      gives you the PROJECT ID  (looks like svc_...)
              |                                                   |
              +-------------------------+-------------------------+
                                        v
  [3] YOUR WORKSTATION      postman-insights-agent  (CLI)
        kubectl get deployment/<name> -o yaml
          | POSTMAN_API_KEY=... postman-insights-agent kube inject --project <projectId> \
          |                                          --repro-mode -s=true -f -
          v  Deployment YAML with the sidecar added  (+ a Secret, because of -s=true)
  [4]   kubectl apply -f -
          |
          v
 +------------------------------ KUBERNETES CLUSTER ----------------------------------+
 |  namespace <your-ns>                                                               |
 |     Secret      postman-agent-secrets     (key: postman-api-key)                   |
 |     Deployment  <your-service>   -> rolling update: new pods replace old ones      |
 |                                                                                    |
 |     +--------------------------- POD  (READY 2/2) -----------------------------+   |
 |     |  +-------------------+   same network namespace   +------------------+   |   |
 |     |  | your app          | <-------------------------> | postman-insights-|   |   |
 |     |  | container         |   (the sidecar sees the     | agent            |   |   |
 |     |  | listens on :PORT  |    pod's HTTP traffic)      | apidump          |   |   |
 |     |  +-------------------+                             | --project <id>   |   |   |
 |     |                                                    | cap: NET_RAW     |   |   |
 |     |                                                    +--------+---------+   |   |
 |     +-----------------------------------------------------------|-------------+   |
 +-----------------------------------------------------------------|-----------------+
                                                                   | [5] outbound HTTPS:
                                                                   |     observed requests
                                                                   v     and responses
 +------------------------------ POSTMAN (your team) -------------------------------+
 |  Insights project  ->  endpoints generated from the traffic                       |
 |  [6] verify: pod shows 2/2, sidecar log is healthy, project fills in              |
 |      (first traffic ~1 minute; endpoints ~5-8 minutes; health checks only = wait) |
 +-----------------------------------------------------------------------------------+
```

## Sidecar or DaemonSet?

| | Sidecar (this guide) | DaemonSet |
|---|---|---|
| Scope | One Deployment at a time; you edit its manifest | One agent per node; every service in the cluster is discovered |
| Setup per service | An Insights **project** and its project ID per service | None; you onboard from the Discovery list |
| Pod change | Yes: the Deployment gets a second container and rolls out | No change to your services |
| Privileges (live examples) | Capability `NET_RAW` only, no host mounts | `NET_RAW`, `SYS_ADMIN`, `SYS_PTRACE`, host `/proc`, network namespaces and the container runtime socket |
| Cost per service | The sidecar's own CPU and memory in every replica | Shared per node |
| Good fit | A few services, a platform team that wants no host access, or a cluster where you can't run a privileged DaemonSet | Many services, or a platform team installing observability once |

Postman recommends the DaemonSet for installs across more than one service.

## Step 1. Prerequisites

- **Kubernetes:** edit permission on the Deployment (and permission to create a Secret in its namespace).
- **Linux:** the agent and your service must both run on Linux.
- **Postman roles:** Workspace Admin or Editor. Creating the project needs Workspace Admin on an **internal** workspace
  (partner and public workspaces don't support Insights).
- **Plain HTTP or TLS?** The docs say the agent observes HTTP and TLS traffic on your ports. Anything the pod doesn't
  send or receive over those ports isn't seen.
- **Network:** the sidecar needs outbound access to Postman. I did not verify the exact hosts.

## Step 2. Create the Insights project and collect two values

1. In Postman, in an internal workspace, click the **Add** icon in the left sidebar and choose **Insights**.
2. Name the project, open its **Setup** tab, and choose **Kubernetes (sidecar)**.
3. Acknowledge the terms. The onboarding screen then shows a ready-made install command with your **project ID** and
   a button to **Add API key**. Copy both.

The project ID looks like `svc_...` (as it appears in the live deployments' `--project` argument). The docs say the
onboarding command already contains the right one and don't say where else to find it.

## Step 3. Install the `postman-insights-agent` CLI

The docs give this command (also shown on the onboarding screen):

```bash
bash -c "$(curl -L https://releases.observability.postman.com/scripts/install-postman-insights-agent.sh)"
```

It downloads and runs a script. If you'd rather read it first, fetch it to a file with `curl -L -o install.sh <url>`,
read it, then run `bash install.sh`.

## Step 4. Inject the sidecar

Find the Deployment:

```bash
kubectl get deployments -A
```

### Option A: the documented one-liner (edits the live Deployment)

```bash
kubectl get -n <namespace> deployment/<deployment> -o yaml \
| POSTMAN_API_KEY=<add-your-api-key-here> postman-insights-agent kube inject --project <projectId> --repro-mode -s=true -f - \
| kubectl apply -f -
```

Expected output:

```
secret/postman-agent-secrets created
deployment.apps/<deployment> configured
```

`-s=true` adds a Secret holding your key to the output, so the same apply creates it. Putting the key in a command-line
prefix leaves it in your shell history; to avoid that, run `read -rs POSTMAN_API_KEY && export POSTMAN_API_KEY` first and
leave the `POSTMAN_API_KEY=...` prefix off, then `unset POSTMAN_API_KEY` afterwards.

### Option B: render to a file and review (for manifest-driven deployments)

Use this when the Deployment is deployed from files in Git (as in this repo): an Option A edit would be **reverted by the
next deploy** because the sidecar isn't in the manifests.

```bash
read -rs POSTMAN_API_KEY && export POSTMAN_API_KEY
postman-insights-agent kube inject --project <projectId> --repro-mode -f k8s/deployment.yaml -o /tmp/deployment.injected.yaml
unset POSTMAN_API_KEY
diff k8s/deployment.yaml /tmp/deployment.injected.yaml     # review exactly what was added
```

Create the Secret yourself in the namespace (the key goes in through stdin, never into a file or Git):

```bash
kubectl create secret generic postman-agent-secrets \
  --namespace <namespace> \
  --from-file=postman-api-key=/dev/stdin
```

Then commit the injected container into the Deployment manifest (or a kustomize patch) and deploy through your normal
pipeline. Don't commit a generated Secret file.

### Useful `kube inject` flags ([reference][docs-inject])

| Flag | What it does |
|---|---|
| `-f`, `--file` | Input Deployment YAML (`-` reads stdin) |
| `-o`, `--output` | Write the result to a file instead of stdout |
| `-s`, `--secret` | `"true"` adds the Secret to the output; a file path writes it to a separate file (requires `--output`) |
| `--project` | Your Insights project ID |
| `--repro-mode` | Send request and response payloads to Postman (see Step 6) |
| `--host-allow`, `--host-exclusions` | Capture only, or skip, HTTP hosts that match these regular expressions |
| `--path-allow`, `--path-exclusions` | Capture only, or skip, HTTP paths that match these regular expressions |
| `--rate-limit` | Requests per minute to capture |
| `--interfaces` | Network interfaces to listen on (default: all) |
| `--proxy`, `--log-format` | HTTP proxy for the agent; log format `color`, `plain` or `json` |

The reference page does not document `--collection`, `--discovery-mode`, namespace flags or image-override flags.

### What the injection adds
The docs don't describe it. This is the sidecar as it appears in live deployments on `se-kube-demo`:

```yaml
- name: postman-insights-agent
  image: public.ecr.aws/postman/postman-insights-agent:latest
  args: ["apidump", "--project", "<projectId>", "--repro-mode"]
  env:
    - name: POSTMAN_INSIGHTS_API_KEY
      valueFrom: {secretKeyRef: {name: postman-agent-secrets, key: postman-api-key}}
    - {name: POSTMAN_K8S_NODE,    valueFrom: {fieldRef: {fieldPath: spec.nodeName}}}
    - {name: POSTMAN_K8S_NAMESPACE, valueFrom: {fieldRef: {fieldPath: metadata.namespace}}}
    - {name: POSTMAN_K8S_POD,     valueFrom: {fieldRef: {fieldPath: metadata.name}}}
    - {name: POSTMAN_K8S_HOST_IP, valueFrom: {fieldRef: {fieldPath: status.hostIP}}}
    - {name: POSTMAN_K8S_POD_IP,  valueFrom: {fieldRef: {fieldPath: status.podIP}}}
  securityContext:
    capabilities: {add: [NET_RAW]}
  resources:
    requests: {cpu: 200m, memory: 500Mi}
    limits:   {cpu: 200m, memory: 500Mi}
```

Notes: the `fieldRef` paths above are exactly those of a live deployment. Another live deployment uses `100m` / `250Mi`,
so the sizes are not fixed. No
`shareProcessNamespace` or `hostPID` is set. One deployment (`aarti-bank-app`) has **two** sidecars: one started with
`apidump --project <id>` and one with `apidump --workspace-id <id> --system-env <id>`. The second form is not in the docs I
read; I mention it only because it exists in the cluster.

## Step 5. Verify the sidecar is running

```bash
kubectl get pods -n <namespace>                                  # READY must show 2/2
kubectl logs -n <namespace> <pod> -c postman-insights-agent      # the sidecar's log
```

Healthy output on a live pod is a stream of `Rotating to new trace on Postman Cloud: <name>` lines. The Deployment rolls
out new pods, so with one replica there is a short switchover.

## Step 6. Send traffic and check Postman

1. Send real HTTP requests to the service. The docs say the agent notices traffic after about a minute, and endpoints take
   **5 to 8 minutes** to appear because they are generated with AI. Only health-check endpoints, or nothing after 10 minutes,
   points to a problem: see Troubleshooting.
2. Open the Insights project. It should fill with the service's endpoints.
3. Optional, **Repro Mode** (replaying requests with real data): only a Workspace Admin can turn it on, in the project's
   **Settings** tab under **Activate Repro Mode**; the `--repro-mode` flag enables it at install time. Postman redacts some
   sensitive values by default, such as auth tokens. **Review what is redacted before enabling it**, and add fields (for
   example a custom API-key header) if needed.

## Applying this to `bookings-api` in this repo

Facts read from the repo and the cluster on 2026-11-17:

- Namespace `gcho`, Deployment `bookings-api`, one replica, app container on port `8080`, plain HTTP. The container image
  runs on an empty base, but the sidecar is a separate container, so the image is untouched.
- **There is no `postman-agent-secrets` Secret in `gcho` yet.**
- Deploys run `kubectl apply -k k8s/` (workflow `deploy-k8s.yml`) from the manifests in `k8s/`. So use **Option B**: an
  Option A edit would disappear at the next deploy.
- The app's requests are `50m` CPU and `32Mi` memory. A sidecar like the live ones (`100m`-`200m`, `250Mi`-`500Mi`) is much
  larger than the app itself; nodes have about 1.9 CPU allocatable, so it fits, but size it deliberately.
- The API reads the `X-API-Key` header. Check Repro Mode's redaction before enabling it, so keys don't land in captured
  payloads.
- **Overlap with the DaemonSet:** the cluster's shared Insights DaemonSet already targets this pod. Running both puts two
  agents on the same traffic; the docs I read don't say whether that is supported, and on `se-kube-demo` several pods with
  sidecars also appear in the DaemonSet agent's logs. Prefer one method per workload unless Postman confirms otherwise.

## Operating notes

- **Pin the image.** The live sidecars use `:latest`, so a pod restart can change the agent version. Pin a tag you trust.
- **The Secret is per namespace and shared.** Every sidecar in the namespace reads the same `postman-agent-secrets`. To
  rotate the key, replace the Secret and restart the Deployments that use it.
- **Remove a sidecar:** deploy the manifest without the container (revert the commit, or re-apply the original YAML).
  Delete the Secret only if no other Deployment in that namespace uses it.
- **Cost:** the agent's CPU and memory are added to every replica of the Deployment.
- **Capture less:** use `--path-exclusions`, `--host-exclusions` or `--rate-limit` to keep noisy or sensitive endpoints out.

## Troubleshooting

| Symptom | What to check |
|---|---|
| Pod shows `1/2` or the sidecar restarts | `kubectl describe pod` and `kubectl logs ... -c postman-insights-agent --previous`. Typical causes: the Secret is missing in that namespace, the image can't be pulled from `public.ecr.aws`, or the resource limits don't fit the node. |
| Pod is `2/2` but the project stays empty | No real traffic yet, or less than about 5 to 8 minutes have passed; the API key or project ID is wrong; no outbound access to Postman. The sidecar log should show errors for the last two. |
| Only health-check endpoints appear | Send real requests through the service's own port. |
| Nothing for HTTPS-only traffic | The docs say TLS traffic on your ports is observed, but I could not confirm how; check Postman's troubleshooting guide for your setup. |
| Anything else | Postman's [Diagnose and troubleshoot Insights Agent errors](https://learning.postman.com/docs/insights/troubleshoot/overview/) page. |
