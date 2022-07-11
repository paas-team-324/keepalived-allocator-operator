#!/bin/bash

# fetch existing GSMs
GSMS_LIST=$(oc get groupsegmentmappings.paas.org -o json)

# replace kind
GSMS_LIST=$(echo $GSMS_LIST | jq '.items[].kind = "IPGroup"')

# filter unwanted fields
for FIELD in metadata.creationTimestamp metadata.generation metadata.resourceVersion metadata.uid spec.keepalivedGroup
do
    GSMS_LIST=$(echo $GSMS_LIST | jq "del(.items[].$FIELD)")
done

# display as YAML
echo $GSMS_LIST | oc create -f - --dry-run=client -o yaml