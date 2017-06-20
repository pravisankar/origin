#!/bin/bash

# OpenShift egress DNS proxy setup script

set -o errexit
set -o nounset
set -o pipefail

# Default DNS nameserver port
NS_PORT=53
CONF=/etc/haproxy/haproxy.cfg

BLANK_LINE_OR_COMMENT_REGEX="([[:space:]]*$|#.*)"
IPADDR_REGEX="[[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+\.[[:digit:]]+"
PORT_REGEX="[[:digit:]]+"
DOMAINNAME_REGEX="[[:alnum:]][[:alnum:]-]+?\.[[:alnum:].-]+"

function die() {
    echo "$*" 1>&2
    exit 1
}

function check_prereqs() {
    if [[ -z "${EGRESS_SOURCE}" ]]; then
        die "Must specify EGRESS_SOURCE"
    fi
    if ! [[ "${EGRESS_SOURCE}" =~ ^${IPADDR_REGEX} ]]; then
        die "EGRESS_SOURCE must be IPv4 address"
    fi

    if [[ -z "${EGRESS_DESTINATION}" ]]; then
        die "Must specify EGRESS_DESTINATION"
    fi
}

function validate_port() {
    local port=$1
    if [[ "${port}" -lt "1" || "${port}" -gt "65535" ]]; then
        die "Invalid port: ${port}, must be in the range 1 to 65535"
    fi
}

function generate_haproxy_defaults() {
    echo "
global
    log         127.0.0.1 local2

    chroot      /var/lib/haproxy
    pidfile     /var/run/haproxy.pid
    maxconn     4000
    user        haproxy
    group       haproxy

defaults
    log                     global
    mode                    tcp
    option                  dontlognull
    option                  tcplog
    option                  redispatch
    retries                 3
    timeout http-request    100s
    timeout queue           1m
    timeout connect         10s
    timeout client          1m
    timeout server          1m
    timeout http-keep-alive 100s
    timeout check           10s
"
}

function generate_dns_resolvers() {
    echo "resolvers dns-resolver"
    # Fetch nameservers from /etc/resolv.conf
    declare -a nameservers=$(cat /etc/resolv.conf |grep nameserver|awk -F" " '{print $2}')
    n=0
    for ns in ${nameservers[@]}; do
        n=$(($n + 1))
        echo "    nameserver ns$n ${ns}:${NS_PORT}"
    done

    # Add google DNS servers as fallback
    echo "    nameserver nsfallback1 8.8.8.8:${NS_PORT}"
    echo "    nameserver nsfallback2 8.8.4.4:${NS_PORT}"

    # Set default optional params
    echo "    resolve_retries      3"
    echo "    timeout retry        1s"
    echo "    hold valid           10s"
    echo ""
}

function generate_haproxy_frontends_backends() {
    local n=0
    declare -A used_ports=()

    while read dest; do
        local port target targetport resolvers 

        if [[ "${dest}" =~ ^${BLANK_LINE_OR_COMMENT_REGEX}$ ]]; then
            continue
        fi
        n=$(($n + 1))
        resolvers=""

        if [[ "${dest}" =~ ^${PORT_REGEX}\ +${IPADDR_REGEX}$ ]]; then
            read port target <<< "${dest}"
            targetport="${port}"
        elif [[ "${dest}" =~ ^${PORT_REGEX}\ +${IPADDR_REGEX}\ +${PORT_REGEX}$ ]]; then
            read port target targetport <<< "${dest}"
        elif [[ "${dest}" =~ ^${PORT_REGEX}\ +${DOMAINNAME_REGEX}$ ]]; then
            read port target <<< "${dest}"
            targetport="${port}"
            resolvers="resolvers dns-resolver"
        elif [[ "${dest}" =~ ^${PORT_REGEX}\ +${DOMAINNAME_REGEX}\ +${PORT_REGEX}$ ]]; then
            read port target targetport <<< "${dest}"
            resolvers="resolvers dns-resolver"
        else
            die "Bad destination '${dest}'"
        fi

        validate_port ${port}
        validate_port ${targetport}

        if [[ "${used_ports[${port}]:-x}" == "x" ]]; then
            used_ports[${port}]=1
        else
            die "Proxy port $port already used, must be unique for each destination"
        fi

        echo "
frontend fe$n
    bind ${EGRESS_SOURCE}:${port}
    default_backend be$n

backend be$n
    server dest$n ${target}:${targetport} check $resolvers
"
    done <<< "${EGRESS_DESTINATION}"
}

function setup_haproxy_config() {
    generate_haproxy_defaults
    generate_dns_resolvers
    generate_haproxy_frontends_backends
}

function setup_haproxy_syslog() {
    cat >> /etc/rsyslog.conf <<EOF
module(load="imudp")
input(type="imudp" port="514")
EOF

    echo "local2.*  /var/log/haproxy.log" >> /etc/rsyslog.d/haproxy.conf

    /usr/sbin/rsyslogd
    touch /var/log/haproxy.log
    tail -f /var/log/haproxy.log &
}

function run() {

    check_prereqs

    rm -f ${CONF}
    setup_haproxy_config > ${CONF}

    setup_haproxy_syslog

    echo "Running haproxy with config:"
    sed -e 's/^/  /' ${CONF}
    echo ""
    echo ""

    exec haproxy -f ${CONF}
 }

if [[ "${EGRESS_DNS_PROXY_MODE:-}" == "unit-test" ]]; then
    check_prereqs
    setup_haproxy_config
    exit 0
fi

run
