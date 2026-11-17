# Step 09 reference: bookings-api and the Postman Insights DaemonSet

Record of an investigation on 2026-11-17. Goal: get the Bookings API (namespace `gcho`, cluster `se-kube-demo`) into the
**Field Services v12 Demo** team's API Catalog through the Insights DaemonSet that is already running.

**Status: not integrated yet.** Everything below was read-only. One Postman write was attempted and blocked by the
permission system, so nothing in Postman or the cluster was changed. Names, pod names and counts below are as of today and
will drift.

## Where things stand

| | |
|---|---|
| Team | Field Services v12 Demo (id `13347347`); user `gunho-cho` (admin, api-catalog-manager) |
| Cluster | EKS `se-kube-demo`, us-east-2 (kubeconfig context `arn:aws:eks:us-east-2:005904641462:cluster/se-kube-demo`) |
| Service | namespace `gcho`, Deployment/Service `bookings-api`, label `app.kubernetes.io/name=bookings-api`, container port 8080 (plain HTTP), Service `LoadBalancer` :80, internet-facing |
| Deployed image | `005904641462.dkr.ecr.us-east-2.amazonaws.com/gcho/bookings-api:cb302caaca2e547c4ee0e1d26f35f127e62bc810` |
| Agent | DaemonSet `postman-insights-agent` in namespace `postman-insights-namespace-fs-v12`, 6/6 Running, image `public.ecr.aws/postman/postman-insights-agent:latest`, args `kube run --repro-mode --discovery-mode`, `POSTMAN_INSIGHTS_CLUSTER_NAME=se-kube-demo`, API key from secret `postman-agent-secrets` (value never read) |
| Other DaemonSet | `rankesh-upi-payment-gateway/postman-insights-agent` is 0/6 ready; not ours |
| Target environment | system environment **DEMO**, id `019e88ba-d6a7-7449-9467-3d7597391014` |
| Repo's Postman workspace | `[gcho demo] bookings api`, id `9bedc149-084e-42b7-9328-00da323328b7` |
| Workspace environments | `deploy` `55002873-f4ead236-36c0-4ec7-b704-ea4af5f9d1ff` (baseUrl = the load balancer), `local` `55002873-176b02b9-5085-489c-a43f-d03fb7e94edc`, `mock` `55002873-6b4dfc78-5130-49da-bbe8-9f225d621327` |

## Findings

1. **Discovery mode needs no change to the service or its manifests.** The agent watches all namespaces except a built-in
   exclusion list. Discovered services show up under *Home > API Catalog > Service Discovery > Postman Insights Catalog*
   and must be onboarded there (service + environment). A service that is not onboarded within a capture window has its
   traffic capture paused; the docs I read do not give the window's length.
2. **The agent sees the pod but cannot register it.** On the node that runs the pod the agent logs, repeatedly:
   `failed to register discovered service: ... status code: 412` for `bookings-api-75f84b9768-5rdzb`. Five of the six
   agents log the same error, and it hits almost every workload in the cluster (payments, identity, orders, js-cart-service,
   ...). So it is systemic, not about bookings-api.
3. **Result:** the team's discovery list (99 entries, read in full) had nothing for `bookings` or `gcho`, and a fresh
   search for "bookings" returned 0. Only 2 entries come from `se-kube-demo`, both last seen in September.
4. **What 412 means is not established.** The Postman docs I could reach do not document it, and the Postman MCP docs
   search timed out. Leads, none confirmed:
   - **Insights upload quota:** `postman_insights_uploads` shows usage of about 1,007,000,000 against a limit of 20,000,000.
   - **Key owner's permissions:** the docs say the key's user needs workspace Admin or Super Admin on the target
     workspaces.
   - **Agent version:** the agent runs `:latest`.
5. **How the catalog models a service (correction to my earlier plan).** A catalog service is a Postman *workspace*. It is
   tied to a system environment when one of the workspace's Postman environments is attached to it. DEMO has 9 such
   attachments, covering 6 services. Registering a "discovered service" through the API does not put it in an environment.

## What I tried and what was blocked

I tried to attach the workspace environment `deploy` to DEMO with the Postman MCP tool
`addApiCatalogSystemEnvironmentAssociations`:

```
systemEnvironmentId:      019e88ba-d6a7-7449-9467-3d7597391014
workspaceEnvironmentIds:  ["55002873-f4ead236-36c0-4ec7-b704-ea4af5f9d1ff"]
allowPartial:             false
```

The permission system denied it, so it did not run. I did not retry or work around it.

I also held back `postApiCatalogDiscoveryServices` (registering a `gcho/bookings-api` discovery record). I have no tool to
delete or archive such a record, it would be a separate `public_api` entry, and it may duplicate the DaemonSet's entry once
that works. Skip it unless you want it.

## What to do

### 1. Fix the 412 (this is what unblocks the DaemonSet path)
- In Postman, check the team's **Resource usage** for Insights uploads and see whether new services are blocked.
- Find who owns the API key the agent uses (Postman > API keys; **do not read the Kubernetes secret**) and confirm that
  user is Admin on the target workspaces and that the key is still valid.
- If neither explains it, contact Postman support (the address is in the agent's own error text,
  `live.insights.alpha@postman.com`) with: cluster `se-kube-demo`, status 412, pod `bookings-api-75f84b9768-5rdzb`, the
  time of the errors, and agent image `:latest`.
- Do not edit the DaemonSet (for example to add `--debug`) without its owner: it serves every service in the cluster.

To see the errors yourself, without switching your kubeconfig context:

```bash
C='arn:aws:eks:us-east-2:005904641462:cluster/se-kube-demo'
kubectl --context "$C" -n postman-insights-namespace-fs-v12 get po -o wide
kubectl --context "$C" -n postman-insights-namespace-fs-v12 logs postman-insights-agent-pf5bl --tail=400 \
  | sed 's/\x1b\[[0-9;]*m//g' | grep -E "bookings-api|status code"
```

(`postman-insights-agent-pf5bl` was the agent on the node running the pod; check `get po -o wide` again, since pod names
change when pods are recreated.)

### 2. Put the workspace into DEMO (works independently of the 412)
Attach the workspace environment `deploy` to the DEMO system environment, either in the API Catalog UI or by allowing the
MCP tool above. I could not verify the exact UI labels for this; the underlying operation is "attach workspace
environments to a system environment". Then check:
- `getApiCatalogSystemEnvironmentAssociations` for DEMO lists the `deploy` environment, and
- `getApiCatalogServices` for DEMO (name filter `bookings`) lists `[gcho demo] bookings api`.

It is reversible with `removeApiCatalogSystemEnvironmentAssociations`. This brings in the workspace, not live traffic.

### 3. Once registration works: send traffic, then onboard
The agent only builds endpoints from real HTTP traffic, and the docs say that takes about 5 to 8 minutes after traffic is
seen. Run the collections against the load balancer (pull `main` first: `deploy.environment.yaml` lives there):

```bash
V=(-e postman/environments/deploy.environment.yaml --no-report-events)
postman collection run "postman/collections/Reset Mock" "${V[@]}"
postman collection run "postman/collections/[Smoke] Bookings API" -i resources -i bookings -i webhook-subscriptions "${V[@]}"
postman collection run "postman/collections/QA Integration Tests" -i Lifecycle "${V[@]}"
```

If the `deploy` environment's `apiKey` (a vault secret) does not resolve, pass `--env-var "apiKey=<the key the Deployment uses>"`
and keep it out of files. Note that "Reset Mock" resets the live pod's state; that is fine for this demo.

Then onboard `gcho/bookings-api` into **DEMO** under *Home > API Catalog > Service Discovery > Postman Insights Catalog*.
Do it promptly, because of the capture window.

## How to know it worked
- The agent log no longer shows `status code: 412` for `bookings-api`, and its state is `TrafficMonitoringRunning`.
- The discovery list shows `gcho/bookings-api` with status `integrated` and source `insights_project`.
- The catalog service list for DEMO includes it, with about 10 endpoints (the spec's operations) once real traffic has been
  captured.

## Risks and notes
- The DaemonSet is shared by the team and runs `:latest`.
- The service is internet-facing over plain HTTP with demo keys and `ENABLE_TEST_ROUTES=true` (the manifests call this
  demo-only). Insights capture works on plain HTTP; HTTPS traffic would not be visible to it.
- Cluster Watcher is a documented alternative that only reads the Kubernetes API and needs no traffic capture. It was not
  part of this investigation.
- Still unconfirmed: the cause of the 412, the length of the capture window, and whether attaching the `deploy`
  environment gives the workspace the catalog entry you want (it is how the existing DEMO services are attached).
