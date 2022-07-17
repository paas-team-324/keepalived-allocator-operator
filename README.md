# VIP Allocator Operator

Kubernetes operator for IP allocation for `LoadBalancer` services.

## Overview

This operator is a solution for `LoadBalancer` service types which is simple to both understand and implement. It allows for allocation of IPs using a preconfigured IP pool. Once the IP is allocated for the service, it is placed in the `.status.loadBalancer.ingress` list which in turn triggers an `iptables` rule to be created on each node. This rule guarantees that if a request's destination IP matches the allocated IP for the service, the request will reach one of the pods behind the service. Therefore, additional routing configuration needs to be done in order for the packet to reach one of the nodes. This process is explained in the `Routing` section below.

Note that:

- only `iptables` kube-proxy implementation is supported
- this operator is not compatible with other `LoadBalancer` solutions on the same cluster
- `nonroot` security context must be allowed for the operator service account

## Deployment

1.  (Disconnected environment) Transfer the following files to your network:
    - Operator image (`docker.io/paasteam324/vip-allocator-operator:<version>`)
    - kube-rbac-proxy image (`gcr.io/kubebuilder/kube-rbac-proxy:v0.5.0`)
    - YAML manifest (`deploy/bundle.yaml`)

2.  Create the namespace for the operator:
    - OpenShift: `oc new-project vip-allocator-operator`
    - Kubernetes: `kubectl create namespace vip-allocator-operator`

3.  (Disconnected environment) Push the relevant images to your disconnected registry and update the `Deployment` object within `deploy/bundle.yaml` with the new image names

4.  Create the operator manifest: `kubectl create -f deploy/bundle.yaml`

5.  (Non-OpenShift) Webhook certificates are handled by OCP 4 service CA feature, which is not present in other kubernetes flavors. You will need to generate the certificates in some other way. Makefile for this project provides a useful `certs` command which generates a long lasting certificate for you.

## Configuration

Now that the operator is deployed, it needs a pool of IPs so they can be allocated to services of type `LoadBalancer`. These pools are defined using `IPGroup` resources. For example:

```yaml
apiVersion: paas.org/v1
kind: IPGroup
metadata:
  name: 1.1.1.0-24
spec:
  segment: "1.1.1.0/24"
  excludedIPs:
  - "1.1.1.0"
  - "1.1.1.255"
```

Creation of the following `IPGroup` will allow the operator to allocate IPs from the `1.1.1.0/24` segment, but will exclude `1.1.1.0` and `1.1.1.255` from allocation.

## Routing

All that's left to do is to configure your network outside the cluster to route packets which are destined to the allocated IPs to your nodes. For example, in order to route packets with destination IP in `1.1.1.0/24` segment, you can do the following:

- Configure IP failover for your nodes to listen on a VIP within the cluster segment, e.g. `1.2.3.4`
- Create the following routing rule within your network: `1.1.1.0/24 via 1.2.3.4`

## Migrating to `1.0` from `0.3`

Version `1.0` removes support for legacy custom resources, specifically `GroupSegmentMapping` and `VirtualIP`. It also removes the unnecessary `cluster-admin` role binding. __Before the upgrade, make sure no `VirtualIP` or `GroupSegmentMapping` objects exist within the cluster__.

In order to perform the upgrade, do the following:

1.  (Disconnected environment) Transfer the following files to your network:
    - Operator image (`docker.io/paasteam324/vip-allocator-operator:<version>`)
    - kube-rbac-proxy image (`gcr.io/kubebuilder/kube-rbac-proxy:v0.5.0`)
    - YAML manifest (`deploy/bundle.yaml`)

2.  (Disconnected environment) Push the relevant images to your disconnected registry and update the `Deployment` object within `deploy/bundle.yaml` with the new image names

3.  Replace the existing manifest with the new one like so:
    ```sh
    kubectl replace -f deploy/bundle.yaml
    ```

4.  Clean-up legacy operator objects like so:
    ```sh
    kubectl delete \
      crd/groupsegmentmappings.paas.org \
      crd/virtualips.paas.org \
      clusterrole/vip-allocator-operator-aggregate-gsms-view \
      clusterrole/vip-allocator-operator-aggregate-virtualips-admin-edit \
      clusterrole/vip-allocator-operator-aggregate-virtualips-view \
      clusterrolebinding/vip-allocator-operator-manager-rolebinding
    ```

5.  (Non-OpenShift) `ValidatingWebhookConfiguration` object will need to be updated with the CA certificate.