package com.epam.reportportal.marketplace.tools;

import org.springframework.security.crypto.bcrypt.BCryptPasswordEncoder;

public class BcryptGen {

  public static void main(String[] args) {
    System.out.println(new BCryptPasswordEncoder().encode("admin"));
  }
}
