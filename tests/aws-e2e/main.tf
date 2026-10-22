locals {
  vms = ["panel", "exit1", "exit2", "entry", "client1", "client2"]
}

data "aws_ssm_parameter" "ubuntu" {
  name = "/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id"
}

resource "aws_vpc" "this" {
  cidr_block           = "10.77.0.0/16"
  enable_dns_hostnames = true
  tags                 = { Name = var.name_prefix }
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
}

resource "aws_subnet" "this" {
  vpc_id                  = aws_vpc.this.id
  cidr_block              = "10.77.1.0/24"
  map_public_ip_on_launch = false
}

resource "aws_route_table" "this" {
  vpc_id = aws_vpc.this.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }
}

resource "aws_route_table_association" "this" {
  subnet_id      = aws_subnet.this.id
  route_table_id = aws_route_table.this.id
}

resource "aws_key_pair" "this" {
  key_name   = "${var.name_prefix}-${terraform.workspace}"
  public_key = var.ssh_public_key
}

resource "aws_security_group" "this" {
  name_prefix = "${var.name_prefix}-"
  vpc_id      = aws_vpc.this.id

  # Everything inside the VPC (panel API, agent <-> panel).
  ingress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = [aws_vpc.this.cidr_block]
  }
  # SSH from the operator only.
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.my_ip_cidr]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_instance" "vm" {
  for_each               = toset(local.vms)
  ami                    = data.aws_ssm_parameter.ubuntu.value
  instance_type          = var.instance_type
  subnet_id              = aws_subnet.this.id
  vpc_security_group_ids = [aws_security_group.this.id]
  key_name               = aws_key_pair.this.key_name
  metadata_options {
    http_tokens = "required"
  }
  root_block_device {
    volume_size = 12
    volume_type = "gp3"
  }
  tags = { Name = "${var.name_prefix}-${each.key}" }
}

# Public addresses so that traffic between VMs egresses via the internet
# gateway and every client presents its own distinct public source IP.
resource "aws_eip" "vm" {
  for_each   = aws_instance.vm
  instance   = each.value.id
  domain     = "vpc"
  depends_on = [aws_internet_gateway.this]
}

# Proxy ports on exits/entry: only from the other test VMs' public addresses.
resource "aws_security_group_rule" "proxy_in" {
  type              = "ingress"
  security_group_id = aws_security_group.this.id
  protocol          = "tcp"
  from_port         = 443
  to_port           = 8443
  cidr_blocks       = [for k in local.vms : "${aws_eip.vm[k].public_ip}/32"]
}

# Marker (9090) and silent hold listener (9092) on the panel, reached from the exits' egress addresses.
resource "aws_security_group_rule" "marker_in" {
  type              = "ingress"
  security_group_id = aws_security_group.this.id
  protocol          = "tcp"
  from_port         = 9090
  to_port           = 9092
  cidr_blocks       = [for k in ["exit1", "exit2"] : "${aws_eip.vm[k].public_ip}/32"]
}
