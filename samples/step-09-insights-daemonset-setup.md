# Set up the Postman Insights agent as a DaemonSet (Discovery Mode)

A walk-through for installing the Postman Insights agent on a Kubernetes cluster so that every service in the cluster is
discovered and can be added to your team's Postman API Catalog.

**What this is based on.** Postman's docs ([Connect with the API Catalog using Postman Insights Agent][docs-connect],
[Install Postman Insights Agent as a DaemonSet][docs-ds], [Install on Kubernetes][docs-k8s]) and the objects of a
working installation I read (read-only) on the `se-kube-demo` EKS cluster on 2026-11-17. I did **not** install a new
DaemonSet while writing this, so the steps are not freshly tested end to end. Where the docs and the live cluster don't
say something, this guide says so instead of guessing.

[docs-connect]: https://learning.postman.com/docs/api-catalog/connect/insights
[docs-ds]: https://learning.postman.com/docs/insights/get-started/kubernetes/daemonset/
[docs-k8s]: https://learning.postman.com/docs/insights/get-started/kubernetes/

## The picture

The numbers match the steps below.

```
  [1] Postman API key  (owner = Admin on the target workspaces)
        |
        |  kubectl create secret ... (key read from stdin)
        v
 +------------------------------ KUBERNETES CLUSTER -------------------------------+
 |                                                                                  |
 |  [2] namespace  postman-insights-namespace                                       |
 |        +- Secret          postman-agent-secrets   (key: postman-api-key)         |
 |        +- ServiceAccount  postman-insights-service-account      } come with the  |
 |        +- ClusterRole + Binding  (read-only: pods get/list/watch) } manifest      |
 |                                                                                  |
 |  [3] DaemonSet  postman-insights-agent        (you edit the downloaded manifest) |
 |        args: kube run --repro-mode --discovery-mode                              |
 |        env : POSTMAN_INSIGHTS_CLUSTER_NAME, POSTMAN_INSIGHTS_API_KEY (<- Secret) |
 |                                                                                  |
 |  [4] kubectl apply  ->  one agent pod on EVERY node                              |
 |                                                                                  |
 |   NODE 1                         NODE 2                      NODE N              |
 |   +--------------------+         +--------------------+      +----------------+  |
 |   | agent pod          |         | agent pod          |      | agent pod      |  |
 |   |  reads host /proc, |         |        ...         | ...  |      ...       |  |
 |   |  netns, containerd |         |                    |      |                |  |
 |   |  socket (read-only)|         |                    |      |                |  |
 |   |  and watches the   |         |                    |      |                |  |
 |   |  traffic of the    |         |                    |      |                |  |
 |   |  pods on this node |         |                    |      |                |  |
 |   +--------------------+         +--------------------+      +----------------+  |
 |   | your app pods      |         | your app pods      |      | your app pods  |  |
 |   +--------------------+         +--------------------+      +----------------+  |
 +-----------|---------------------------------|------------------------|-----------+
             +-----------------+---------------+------------------------+
                               |  [5] outbound HTTPS: each workload is registered as
                               |      cluster/namespace/workload  (status: discovered)
                               v
 +----------------------------- POSTMAN (your team) ------------------------------+
 |  Home > API Catalog > Service Discovery > Postman Insights Catalog              |
 |      list of discovered services                                                |
 |                                                                                 |
 |  [6] you pick a service + a system environment (for example DEMO)  -> onboard   |
 |                                                                                 |
 |      status: integrated  -> a service in that environment of the API Catalog    |
 |      endpoints are built from real HTTP traffic (about 5-8 minutes after it)    |
 +---------------------------------------------------------------------------------+
   Not onboarded within the capture window?  The agent pauses capture for that service.
```

## Step 1. Prerequisites

| Need | Detail |
|---|---|
| Kubernetes | 1.19 or later (docs). `kubectl` pointed at the cluster. |
| Rights in the cluster | Enough to create a namespace, a `ClusterRole`/`ClusterRoleBinding`, a Secret, and a DaemonSet that adds Linux capabilities and mounts host paths. In practice, cluster admin. |
| Nodes | Linux nodes. The agent in the working cluster reads the container runtime at `/var/run/containerd/containerd.sock`, so the nodes run **containerd**. Other runtimes are not covered here; check the docs for the CRI endpoint setting (`POSTMAN_INSIGHTS_CRI_ENDPOINT`). |
| Pod security | If the cluster enforces Pod Security Standards or a policy engine, the agent's namespace must allow added capabilities (`NET_RAW`, `SYS_ADMIN`, `SYS_PTRACE`) and `hostPath` volumes. (The `se-kube-demo` agent namespace has no enforcement labels.) |
| Network | The agent pods need outbound access to Postman's API. I did not verify the exact hosts. |
| Postman | A team with API Catalog (Enterprise) and Insights. A **Postman API key** whose user has write access to the target workspaces (workspace Admin or Super Admin, per the docs): the agent creates and links applications on that user's behalf. |
| A cluster name | A stable, unique string such as `se-kube-demo`. It becomes part of every service's identity (`cluster/namespace/workload`). **Changing it later makes every service appear as a new entry.** |

## Step 2. Create the namespace and the API key secret

```bash
kubectl create namespace postman-insights-namespace
kubectl create secret generic postman-agent-secrets \
  --namespace postman-insights-namespace \
  --from-file=postman-api-key=/dev/stdin
```

The second command waits for input: paste the API key, press Enter, then Ctrl-D. Reading it from stdin keeps the key out of
your shell history and out of any file. The secret name (`postman-agent-secrets`) and key (`postman-api-key`) must match
what the DaemonSet manifest references. If you use a different namespace name, change it everywhere in the manifest.

## Step 3. Get the manifest and make three edits

Download `postman-insights-agent-daemonset.yaml` from the [DaemonSet install page][docs-ds] (the link is on that page; I
could not retrieve the direct URL). It creates the namespace, the service account, a read-only cluster role and binding,
and the `postman-insights-agent` DaemonSet. Applying over the namespace from step 2 is fine.

Edit it:

1. **Add `--discovery-mode`** to the container `args`, after `kube` and `run`. Keep the other flags: Postman says
   `--discovery-mode` and `--repro-mode` are not alternatives.
   ```yaml
   args: ["kube", "run", "--repro-mode", "--discovery-mode"]
   ```
2. **Set the cluster name:**
   ```yaml
   - name: POSTMAN_INSIGHTS_CLUSTER_NAME
     value: "se-kube-demo"     # your stable, unique name
   ```
3. **Read the API key from the secret** (not a literal value in the YAML):
   ```yaml
   - name: POSTMAN_INSIGHTS_API_KEY
     valueFrom:
       secretKeyRef:
         name: postman-agent-secrets
         key: postman-api-key
   ```

Optional: limit discovery to some namespaces by adding `--include-namespaces=production,staging` to `args`. By default the
agent covers all namespaces except a built-in exclusion list (Kubernetes system namespaces, monitoring and GitOps tools).

### Reference: what a working installation looks like
Reconstructed from the objects running on `se-kube-demo`. **The official download is authoritative.** This omits details I
didn't read, such as the `type` of each `hostPath`.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata: {name: postman-insights-service-account, namespace: postman-insights-namespace}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: postman-insights-read-only-role}
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "watch", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: postman-insights-view-all-resources-binding}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: postman-insights-read-only-role}
subjects:
  - {kind: ServiceAccount, name: postman-insights-service-account, namespace: postman-insights-namespace}
---
apiVersion: apps/v1
kind: DaemonSet
metadata: {name: postman-insights-agent, namespace: postman-insights-namespace}
spec:
  selector: {matchLabels: {name: postman-insights-agent}}
  updateStrategy: {type: RollingUpdate, rollingUpdate: {maxUnavailable: 30%}}
  template:
    metadata: {labels: {name: postman-insights-agent}}
    spec:
      serviceAccountName: postman-insights-service-account
      containers:
        - name: postman-insights-agent
          image: public.ecr.aws/postman/postman-insights-agent:latest
          args: ["kube", "run", "--repro-mode", "--discovery-mode"]
          env:
            - {name: POSTMAN_INSIGHTS_CLUSTER_NAME, value: "se-kube-demo"}
            - name: POSTMAN_INSIGHTS_API_KEY
              valueFrom: {secretKeyRef: {name: postman-agent-secrets, key: postman-api-key}}
            - name: POSTMAN_INSIGHTS_K8S_NODE
              valueFrom: {fieldRef: {fieldPath: spec.nodeName}}
            - {name: POSTMAN_INSIGHTS_CRI_ENDPOINT, value: /var/run/containerd/containerd.sock}
          securityContext:
            capabilities: {add: [NET_RAW, SYS_ADMIN, SYS_PTRACE], drop: [ALL]}
          resources:
            requests: {cpu: 200m, memory: 500Mi}
            limits:   {cpu: 200m, memory: 500Mi}
          volumeMounts:
            - {name: host-procfs,      mountPath: /host/proc,                       readOnly: true}
            - {name: host-netns,       mountPath: /host/var/run/netns,              readOnly: true}
            - {name: containerdsocket, mountPath: /var/run/containerd/containerd.sock, readOnly: true}
      volumes:
        - {name: host-procfs,      hostPath: {path: /proc}}
        - {name: host-netns,       hostPath: {path: /var/run/netns}}
        - {name: containerdsocket, hostPath: {path: /var/run/containerd/containerd.sock}}
```

## Step 4. Apply and check the agents are up

```bash
kubectl apply -f postman-insights-agent-daemonset.yaml
kubectl get daemonset -n postman-insights-namespace          # DESIRED must equal READY
kubectl get pods -n postman-insights-namespace -o wide        # one Running pod per node
kubectl logs -n postman-insights-namespace -l name=postman-insights-agent --tail=50
```

- **DESIRED = READY** means an agent runs on every node. If READY is lower, see the troubleshooting table.
- **Healthy logs** contain a periodic table of monitored pods whose `currentState` is `TrafficMonitoringRunning`.
- **A registration problem** shows as `failed to register discovered service ... status code: NNN` (see Troubleshooting).

## Step 5. Discovery happens by itself

Nothing to configure per service. Within a few minutes each workload (Deployment, StatefulSet, ...) should appear in
Postman under **Home > API Catalog > Service Discovery > Postman Insights Catalog**, named like `namespace/workload`
(from the live catalog, with `cluster_name`, `namespace`, `workload_name`, `workload_type` and `discovery_mode: daemonset`
in its metadata). Discovered services are **not** in the catalog yet.

## Step 6. Send traffic, then onboard the service

1. **Send real HTTP traffic** to the service. The agent builds endpoints from observed requests (about 5 to 8 minutes after
   traffic is seen, per the docs). Only health checks means you'll see only health endpoints. Plain HTTP is visible to the
   agent; TLS-encrypted traffic is not.
2. In *Postman Insights Catalog*, select the service and the **system environment** to put it in (for example `DEMO`), and
   onboard it. The API key's user needs write access to the target workspace.
3. Do this before the **capture window** ends. A service not onboarded in time has its capture paused by the agent;
   onboarding lifts that. The docs I read do not state the window's length.

## Verify it worked

- The agent log shows `TrafficMonitoringRunning` for your workload and no registration errors.
- The service's status in the discovery list is `integrated`, with source `insights_project`.
- The system environment's service list includes the service, with endpoints that match what your clients call.

## Operating notes

- **Pin the agent image.** The manifest uses `:latest`, so a node restart can change the agent version silently. Pin a tag
  once you've picked a version you trust.
- **One DaemonSet per cluster.** Two agents covering the same cluster (the live cluster has a second, unhealthy one in
  another namespace) can register duplicates.
- **Update:** change the manifest and `kubectl apply`, or `kubectl rollout restart daemonset/postman-insights-agent -n postman-insights-namespace`.
- **Rotate the API key:** replace the secret, then restart the DaemonSet.
- **Uninstall:** delete the DaemonSet, then the `ClusterRoleBinding`, `ClusterRole` and namespace. Entries already in the
  catalog stay until you remove them in Postman.
- **Security:** the agent runs with `SYS_ADMIN`, `SYS_PTRACE` and `NET_RAW`, and mounts host `/proc`, the network
  namespaces and the container runtime socket (read-only). Treat the namespace as privileged. With Repro Mode on, request
  and response payloads are captured; Postman redacts some sensitive values (such as auth tokens) by default, and a
  Workspace Admin controls what else is redacted.
- **Trace edges:** for service-to-service links in the catalog graph, your services must forward W3C `traceparent` headers.
  The agent does not add them.
- **Alternative:** *Cluster Watcher* reads only the Kubernetes API (no traffic capture, no host access). It is a separate
  option and not covered here.

## Troubleshooting

| Symptom | What to check |
|---|---|
| DaemonSet READY < DESIRED | Taints on some nodes (the manifest has no tolerations), pod security policy blocking the capabilities or `hostPath`, image pull from `public.ecr.aws` blocked, or CPU/memory limits (`200m`/`500Mi`) not fitting on a full node. `kubectl describe pod` shows which. |
| Pods Running, nothing in Postman | The API key is wrong or revoked, the key's user lacks write access to the target workspaces, no outbound access to Postman, or the namespace is in the agent's exclusion list or outside `--include-namespaces`. |
| Log: `failed to register discovered service ... status code: 412` | Seen on `se-kube-demo`, hitting almost every workload, so it is a team- or key-level problem, not the service. **The meaning is not established.** Leads, unconfirmed: the team's Insights upload quota (usage far above its limit at the time) and the key owner's permissions. Check Postman's Resource usage and the key owner, then contact Postman support (address in the error text) with the cluster name, status, a pod name and the time. |
| Log: `Failed to send telemetry: Postman Insights verification token is empty` | Seen alongside the 412 above. Its significance is unconfirmed. |
| Service discovered but no endpoints | No traffic yet, only health checks, traffic is TLS-encrypted, or less than about 5 to 8 minutes have passed. |
| Service disappears from capture | It was not onboarded within the capture window; send traffic and onboard it. |
| Everything shows up again as new | `POSTMAN_INSIGHTS_CLUSTER_NAME` was changed. Use the old value, or accept the new entries. |
