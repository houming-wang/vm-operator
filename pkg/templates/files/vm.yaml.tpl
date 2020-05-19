heat_template_version: 2016-10-14

description: >
  This template will create server resources

parameters:

  name:
    type: string
    description:

  image:
    type: string
    description:

  flavor:
    type: string
    description:

  availability_zone:
    type: string
    description:
    default: nova

  key_name:
    type: string
    description:
    default: default

  admin_pass:
    type: string
    description:

  boot_volume_type:
    type: string
    description:

  boot_volume_size:
    type: string
    description:

  security_group:
    type: string
    description:

  fixed_network:
    type: string
    description:

  fixed_subnet:
    type: string
    description:

  neutron_az:
    type: string
    description:
    default: []
{{- if .network.floating_ip -}}
{{ if eq .network.floating_ip "enable" }}
  external_network:
    type: string
    description:
    default: public

  floating_ip_bandwidth:
    type: string
    description:
{{ end }}
{{- end -}}

{{ range $index, $v := .volume }}
  volume_{{ $index }}_name:
    type: string
    default: {{ $v.volume_name }}

  volume_{{ $index }}_type:
    type: string
    default: {{ $v.volume_type }}

  volume_{{ $index }}_size:
    type: string
    default: {{ $v.volume_size}}
{{ end }}

resources:

  ######################################################################
  #
  # vitual machines
  #

{{ if .softwareConfig }}
  software_config:
    type: OS::Heat::SoftwareConfig
    properties:
      group: ungrouped
      config: |
{{ indent 8 .softwareConfig }}

  node_bootstrap:
    type: OS::Heat::MultipartMime
    properties:
      parts:
        - config: {get_resource: software_config}
{{ end }}

  node_boot_volume:
    type: OS::Cinder::Volume
    properties:
      image: {get_param: image}
      size: {get_param: boot_volume_size}
      volume_type: {get_param: boot_volume_type}

  mixapp_node:
    type: OS::Nova::Server
    properties:
      name: {get_param: name}
      flavor: {get_param: minion_flavor}
      key_name: {get_param: ssh_key_name}
      admin_pass: {get_param: admin_pass}
      user_data_format: RAW
      user_data: {get_resource: kube_minion_init}
      networks:
        - port: {get_resource: kube_minion_eth0}
      scheduler_hints: { group: { get_param: nodes_server_group_id }}
      availability_zone: {get_param: availability_zone}
      block_device_mapping_v2:
        - boot_index: 0
          volume_id: {get_resource: kube_node_volume}
          delete_on_termination: true

  node_eth0:
    type: OS::Neutron::Port
    properties:
      network: {get_param: fixed_network}
      security_groups:
        - get_param: security_group
      fixed_ips:
        - subnet: {get_param: fixed_subnet}
      allowed_address_pairs:
        - ip_address: {get_param: pods_network_cidr}
      replacement_policy: AUTO

  ######################################################################
  #
  # floating ip
  #
{{- if .network.floating_ip -}}
{{ if eq .network.floating_ip "enable" }}
  node_qos_policy:
    type: OS::Neutron::QoSPolicy

  node_floating_ip_qosbandwidthrule:
    type: OS::Neutron::QoSBandwidthLimitRule
    properties:
      policy: {get_resource: node_qos_policy}
      max_kbps: {get_param: floating_ip_bandwidth}

  node_floating:
    type: OS::Neutron::FloatingIP
    properties:
      floating_network: {get_param: external_network}
      port_id: {get_resource: node_eth0}
      qos_policy: {get_resource: node_qos_policy}
{{ end }}
{{- end -}}

  ######################################################################
  #
  # data volumes
  #

{{ range $index, $v := .volume -}}
{{ $char := add $index 98 }}
  data_volume_{{ $index }}:
    type: OS::Cinder::Volume
    properties:
      name: {get_param: volume_{{ $index }}_name}
      size: {get_param: volume_{{ $index }}_size}
      volume_type: {get_param: volume_{{ $index }}_type}

  data_volume_attach_{{ $index }}:
    type: OS::Cinder::VolumeAttachment
    properties:
      instance_uuid: {get_resource: mixapp_node}
      volume_id: {get_resource: data_volume_{{ $index }}}
      mountpoint: /dev/vd{{ toChar $char }}
{{- end }}
