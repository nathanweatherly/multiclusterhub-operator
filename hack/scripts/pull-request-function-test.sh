#!/bin/bash
# Copyright (c) 2020 Red Hat, Inc.
# Copyright Contributors to the Open Cluster Management project

function delete_cluster() {
    oc get pods
    echo "Deleting clusterclaim ..."
    oc login --token="${COLLECTIVE_TOKEN}" --server="${COLLECTIVE_SERVER}"  --insecure-skip-tls-verify

    cd ./lifeguard/clusterclaims/
    echo "Y" | ./delete.sh
    echo "Tests failed. Cluster deleted."
    exit 1
}

set -e

export CLUSTERCLAIM_LIFETIME=4h
export CLUSTERPOOL_TARGET_NAMESPACE=install
export CLUSTERPOOL_NAME=installer-function-test
export CLUSTERCLAIM_GROUP_NAME=Installer
export CLUSTERCLAIM_NAME=install-function-test

export COLLECTIVE_SERVER=https://api.collective.aws.red-chesterfield.com:6443

if [[ -z "${COLLECTIVE_TOKEN}" ]]; then
    echo "environment variable 'COLLECTIVE_TOKEN' must be set"
    exit 1
fi

if ! command -v yq &> /dev/null
then
    echo "Installing yq ..."
    wget https://github.com/mikefarah/yq/releases/download/v4.9.3/yq_linux_amd64.tar.gz -O - |\
  tar xz && sudo mv yq_linux_amd64 /usr/bin/yq >/dev/null
fi

_OPERATOR_SDK_VERSION=v0.18.2

if ! [ -x "$(command -v operator-sdk)" ]; then
    echo "Installing Operator-SDK ..."
    if [[ "$OSTYPE" == "linux-gnu" ]]; then
            curl -L https://github.com/operator-framework/operator-sdk/releases/download/${_OPERATOR_SDK_VERSION}/operator-sdk-${_OPERATOR_SDK_VERSION}-x86_64-linux-gnu -o operator-sdk
    elif [[ "$OSTYPE" == "darwin"* ]]; then
            curl -L https://github.com/operator-framework/operator-sdk/releases/download/${_OPERATOR_SDK_VERSION}/operator-sdk-${_OPERATOR_SDK_VERSION}-x86_64-apple-darwin -o operator-sdk
    fi
    chmod +x operator-sdk
    sudo mv operator-sdk /usr/local/bin/operator-sdk
fi

if ! command -v ginkgo &> /dev/null
then
    echo "Installing ginkgo ..."
    go install github.com/onsi/ginkgo/ginkgo
fi

oc login ${COLLECTIVE_SERVER} --insecure-skip-tls-verify --token="${COLLECTIVE_TOKEN}"

git clone https://github.com/stolostron/lifeguard.git

cd lifeguard/clusterclaims/

READY_CLUSTERS=$(oc get clusterpool installer-function-test -o yaml | yq eval '.status.ready' -)
if [ "$READY_CLUSTERS" -eq "0" ]; then
   echo "No clusterpool clusters available currently. Please try again later ..."
   exit 1
fi

echo "Applying clusterclaim ..."
./apply.sh
set +e
trap 'delete_cluster' ERR

cd ../..

attempts=0
until [ "$attempts" -ge 10 ]
do
    echo "Attempting to login to cluster ..."
    oc login $(jq -r '.api_url' ./lifeguard/clusterclaims/${CLUSTERCLAIM_NAME}/${CLUSTERCLAIM_NAME}.creds.json) -u kubeadmin -p $(jq -r '.password' ./lifeguard/clusterclaims/${CLUSTERCLAIM_NAME}/${CLUSTERCLAIM_NAME}.creds.json) --insecure-skip-tls-verify=true && break
    if [ "$attempts" -eq "10" ]; then
        echo "Unable to login to cluster ... Please try again later"
        exit 1
    fi
    attempts=$((attempts+1))
    sleep 15
done

oc project

make prep-mock-install MOCK_IMAGE_REGISTRY='quay.io/stolostron' MOCK_IMAGE_NAME='multiclusterhub-operator' MOCK_IMAGE_TAG='mock'
make mock-install REGISTRY='quay.io/stolostron' VERSION='mock-img' MOCK_IMAGE_REGISTRY='quay.io/stolostron' MOCK_IMAGE_NAME='multiclusterhub-operator' MOCK_IMAGE_TAG='mock'
make ft-install MOCK=true

echo "Deleting clusterclaim ..."
oc login --token="${COLLECTIVE_TOKEN}" --server="${COLLECTIVE_SERVER}"  --insecure-skip-tls-verify

cd ./lifeguard/clusterclaims/
echo "Y" | ./delete.sh

echo "Pull request function tests completed successfully!"
exit 0