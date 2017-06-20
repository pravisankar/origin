#
# This is the egress router L4 DNS proxy for OpenShift Origin
#
# The standard name for this image is openshift/origin-egress-dns-proxy

FROM openshift/origin-base

# HAProxy 1.6+ version is needed to leverage DNS resolution at runtime.
RUN INSTALL_PKGS="rsyslog gcc make openssl-devel pcre-devel tar wget socat" && \
    yum install -y $INSTALL_PKGS && \
    rpm -V $INSTALL_PKGS && \
    wget http://www.haproxy.org/download/1.7/src/haproxy-1.7.5.tar.gz && \
    tar xvzf haproxy-1.7.5.tar.gz && \
    groupadd haproxy && \
    useradd -g haproxy haproxy && \
    cd haproxy-* && make TARGET=linux2628 CPU=native USE_PCRE=1 USE_OPENSSL=1 USE_ZLIB=1 && make install && \
    cd .. && rm -rf haproxy-* && \
    setcap 'cap_net_bind_service=ep' /usr/local/sbin/haproxy && \
    mkdir -p /var/lib/haproxy && \
    mkdir -p /etc/haproxy && \
    touch /etc/haproxy/haproxy.cfg && \
    yum -y remove gcc && \
    yum clean all

ADD egress-dns-proxy.sh /bin/egress-dns-proxy.sh

ENTRYPOINT /bin/egress-dns-proxy.sh

