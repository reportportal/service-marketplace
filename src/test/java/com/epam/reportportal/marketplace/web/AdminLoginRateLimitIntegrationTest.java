package com.epam.reportportal.marketplace.web;

import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.webmvc.test.autoconfigure.AutoConfigureMockMvc;
import org.springframework.http.MediaType;
import org.springframework.test.context.TestPropertySource;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest
@AutoConfigureMockMvc
@TestPropertySource(properties = {
    "marketplace.auth.login-rate-limit.enabled=true",
    "marketplace.auth.login-rate-limit.max-attempts=3",
    "marketplace.auth.login-rate-limit.lockout-seconds=600",
    "marketplace.auth.login-rate-limit.window-seconds=3600"
})
class AdminLoginRateLimitIntegrationTest {

  @Autowired
  private MockMvc mockMvc;

  @Test
  void repeatedFailedLoginsReturnTooManyRequests() throws Exception {
    String body = "{\"username\":\"admin\",\"password\":\"wrong-password\"}";
    for (int i = 0; i < 3; i++) {
      mockMvc.perform(post("/api/v1/auth/login")
              .contentType(MediaType.APPLICATION_JSON)
              .content(body))
          .andExpect(status().isUnauthorized());
    }

    mockMvc.perform(post("/api/v1/auth/login")
            .contentType(MediaType.APPLICATION_JSON)
            .content(body))
        .andExpect(status().isTooManyRequests())
        .andExpect(jsonPath("$.code").value("TOO_MANY_REQUESTS"));
  }
}
