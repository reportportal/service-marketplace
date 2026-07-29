package com.epam.reportportal.marketplace.auth;

import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.webmvc.test.autoconfigure.AutoConfigureMockMvc;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest
@AutoConfigureMockMvc
class GitHubOAuthUnconfiguredTest {

  @Autowired
  private MockMvc mockMvc;

  @Test
  void loginIsUnavailableWhenOAuthIsNotConfigured() throws Exception {
    mockMvc.perform(get("/api/v1/auth/github/login"))
        .andExpect(status().isServiceUnavailable())
        .andExpect(jsonPath("$.code").value("SERVICE_UNAVAILABLE"));
  }

  @Test
  void callbackIssuesNoSessionWhenOAuthIsNotConfigured() throws Exception {
    mockMvc.perform(get("/api/v1/auth/github/callback")
            .param("code", "any-code")
            .param("state", "any-state"))
        .andExpect(status().isServiceUnavailable())
        .andExpect(jsonPath("$.code").value("SERVICE_UNAVAILABLE"))
        .andExpect(jsonPath("$.accessToken").doesNotExist());
  }
}
