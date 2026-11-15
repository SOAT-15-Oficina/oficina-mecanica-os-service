resource "aws_lb_target_group" "service" {
  name        = substr(local.name, 0, 32)
  port        = 8080
  protocol    = "HTTP"
  vpc_id      = local.vpc_id
  target_type = "ip"

  health_check {
    path                = "/ready"
    interval            = 15
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
    matcher             = "200"
  }
}

resource "aws_lb_listener_rule" "service" {
  listener_arn = local.alb_listener_arn
  priority     = var.listener_rule_priority

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.service.arn
  }

  condition {
    path_pattern {
      values = [var.path_pattern]
    }
  }
}
