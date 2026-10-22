output "public_ip" {
  value = { for k, e in aws_eip.vm : k => e.public_ip }
}

output "private_ip" {
  value = { for k, i in aws_instance.vm : k => i.private_ip }
}
