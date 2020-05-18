heat_template_version: 2016-10-14

description: >
  This template will boot a stack with one or more servers
  for mixed applications orchestration

parameters:

  replicas:
    type: number
    description:
    default: 1

  name_prefix:
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

{{ if .network.existing_subnet }}
  existing_network:
    type: string
    description

  existing_subnet:
    type: string
    description:
{{ else }}
  private_network_cidr:
    type: string
    description:

  private_network_name:
    type: string
    description:
{{ end }}

  neutron_az:
    type: string
    description:
    default: []

{{ if .network.floating_ip == "enable" }}
  external_network:
    type: string
    description:
    default: public

  floating_ip:
    type: string
    description:

  floating_ip_bandwidth:
    type: string
    description:
{{ end }}


resources:

  ######################################################################
  #
  # network resources
  #

  network:
    type: network.yaml
    properties:
    {{ if .network.existing_subnet }}
      existing_network: {get_param: fixed_network}
      existing_subnet: {get_param: fixed_subnet}
    {{ else }}
      private_network_name: {get_param: private_network_name}
      private_network_cidr: {get_param: private_network_cidr}
    {{- end -}}
    {{- if .network.floating_ip == "enable" }}
      external_network: {get_param: external_network}
    {{- end -}}
      neutron_az: {get_param: neutron_az}
      private_network_name: ecns-private


  ######################################################################
  #
  # security groups.
  #



  ######################################################################
  #
  # vitual machines
  #

  mixapp_nodes:
    type: OS::Heat::ResourceGroup
    depends_on:
      - network
    properties:
      count: {get_param: replicas}
      resource_def:
        type: vm.yaml
        properties:
          name:
            list_join:
              - '-'
              - [{ get_param: namePrefix }, '%index%']
          image: {get_param: image}
          flavor: {get_param: flavor}
          availability_zone: {get_param: availability_zone}
          key_name: {get_param: key_name}
          admin_pass: {get_param: admin_pass}
          boot_volume_type: {get_param: boot_volume_type}
          boot_volume_size: {get_param: boot_volume_size}
          security_group: {get_param: security_group}
          fixed_network: {get_attr: [network, fixed_network]}
          fixed_subnet: {get_attr: [network, fixed_network]}
        {{- if .network.floating_ip == "enable" -}}
          external_network: {get_param: external_network}
          floating_ip_bandwidth: {get_param: floating_ip_bandwidth}
        {{- end -}}
        {{- range $index, $v := .volume -}}
          volume_{{ $index }}_name: "{{ $v.volume_name }}"
          volume_{{ $index }}_type: "{{ $v.volume_type }}"
          volume_{{ $index }}_size: "{{ $v.volume_size }}"
        {{- end -}}

outputs:

  subnet:
    value: {get_attr: [network, fixed_subnet]}
    description: >
      This is the subnet of this kube cluster used.

  network:
    value: {get_attr: [network, fixed_network]}
    description: >
      This is the network of this kube cluster used.



