#!/bin/bash
set -ex
source $(dirname $0)/provision-config.sh

NODE_IP=$4
OPENSHIFT_SDN=$6
NODE_INDEX=$5

NETWORK_CONF_PATH=/etc/sysconfig/network-scripts/
sed -i 's/^NM_CONTROLLED=no/#NM_CONTROLLED=no/' ${NETWORK_CONF_PATH}ifcfg-eth1

systemctl restart network

# get the node name, index is 1-based
node_name=${NODE_NAMES[$NODE_INDEX-1]}

# Setup hosts file to support ping by hostname to master
if [ ! "$(cat /etc/hosts | grep $MASTER_NAME)" ]; then
  echo "Adding $MASTER_NAME to hosts file"
  echo "$MASTER_IP $MASTER_NAME" >> /etc/hosts
fi

# Setup hosts file to support ping by hostname to each node in the cluster
node_ip_array=(${NODE_IPS//,/ })
for (( i=0; i<${#NODE_NAMES[@]}; i++)); do
  node=${NODE_NAMES[$i]}
  ip=${node_ip_array[$i]}  
  if [ ! "$(cat /etc/hosts | grep $node)" ]; then
    echo "Adding $node to hosts file"
    echo "$ip $node" >> /etc/hosts
  fi
done
if ! grep ${NODE_IP} /etc/hosts; then
  echo "${NODE_IP} ${node_name}" >> /etc/hosts
fi

# Install the required packages
yum install -y docker-io git golang e2fsprogs hg openvswitch net-tools bridge-utils which ethtool

# Build openshift
echo "Building openshift"
pushd /vagrant
  ./hack/build-go.sh
  cp _output/local/go/bin/openshift /usr/bin
popd

# Copy over the certificates directory
cp -r /vagrant/openshift.local.config /
chown -R vagrant.vagrant /openshift.local.config

mkdir -p /openshift.local.volumes

# Setup SDN
$(dirname $0)/provision-sdn.sh $@

# Create systemd service
cat <<EOF > /usr/lib/systemd/system/openshift-node.service
[Unit]
Description=OpenShift Node
Requires=network.service
After=docker.service network.service

[Service]
ExecStart=/usr/bin/openshift start node --config=/openshift.local.config/node-${node_name}/node-config.yaml
Restart=on-failure
RestartSec=10s

[Install]
WantedBy=multi-user.target
EOF

# Start the service
systemctl daemon-reload
systemctl enable openshift-node.service
systemctl start openshift-node.service

# Set up the KUBECONFIG environment variable for use by the client
echo 'export KUBECONFIG=/openshift.local.config/master/admin.kubeconfig' >> /root/.bash_profile
echo 'export KUBECONFIG=/openshift.local.config/master/admin.kubeconfig' >> /home/vagrant/.bash_profile
