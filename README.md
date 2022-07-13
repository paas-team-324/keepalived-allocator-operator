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

## Migration from `VirtualIP` resources

In the early versions of the operator, IP exposure relied on the [keepalived-operator](https://github.com/redhat-cop/keepalived-operator). Since the architectural change, `VirtualIP` and `GroupSegmentMapping` resources have been deprecated. In order to allow graceful migration from those resources, migration process has been introduced. Version `0.3` of the operator is an intermediate version which reconciles both `VirtualIP` and `Service` objects of type `LoadBalancer` and allows for migration from former to latter using an annotation.

Before migration:
- upgrade to version `0.3` of the operator
- make sure the service exposed by the `VirtualIP` is not already of type `LoadBalancer`

In order to migrate from `VirtualIP`, annotate the object like so:
```sh
kubectl annotate vip <vip-name> -n <namespace> virtualips.paas.org/migrate=""
```

The target service type will be converted to `LoadBalancer` and the service itself will be reconciled by the new controller. The migration process deletes the exposed service clone and then assigns the IP back to the original service, __which results in a momentary disruption__. Once the `VirtualIP` reports `Migrated` state, it can be safely deleted.

## Migrating to `0.3`

Intermediate version `0.3` introduces the new service controller, but still reconciles the `VirtualIP` objects, forbidding creation of new ones in favor of migration. This version also introduces a name change for the namespace as well as the objects which make up the operator. In order to perform the upgrade, do the following:

1.  (Disconnected environment) Transfer the following files to your network:
    - Operator image (`docker.io/paasteam324/vip-allocator-operator:<version>`)
    - kube-rbac-proxy image (`gcr.io/kubebuilder/kube-rbac-proxy:v0.5.0`)
    - YAML manifest (`deploy/bundle.yaml`)
    - Migration manifest (`deploy/0.3_migration/0.3_migration_delete_bundle.yaml`)
    - GSM to IPGroup helper script (`deploy/0.3_migration/generate_ipgroups.sh`)

2.  Create the namespace for the operator:
    - OpenShift: `oc new-project vip-allocator-operator`
    - Kubernetes: `kubectl create namespace vip-allocator-operator`

3.  (Disconnected environment) Push the relevant images to your disconnected registry and update the `Deployment` object within `deploy/bundle.yaml` with the new image names

4.  Use migration bundle to clean-up old operator objects without deleting the custom resources like so: `kubectl delete -f deploy/0.3_migration/0.3_migration_delete_bundle.yaml`. __This action will remove the current operator, meaning the existing objects will not be reconciled until step 5 below is performed.__

5.  Create the new operator manifest: `kubectl create -f deploy/bundle.yaml`. `CustomResourceDefinition` objects which already exist (specifically `groupsegmentmappings.paas.org`, `ips.paas.org` and `virtualips.paas.org`) will report `AlreadyExists` error which is normal.

6.  Subsequent versions of the operator are using the new `IPGroup` custom resource to allocate IPs to services. Each existing `GroupSegmentMapping` resource will need an `IPGroup` alternative. In order to create these alternatives, you can run the following commands:
    ```sh
    ./deploy/0.3_migration/generate_ipgroups.sh | kubectl create -f -
    ```

7.  (Non-OpenShift) `ValidatingWebhookConfiguration` object will need to be updated with the CA certificate.