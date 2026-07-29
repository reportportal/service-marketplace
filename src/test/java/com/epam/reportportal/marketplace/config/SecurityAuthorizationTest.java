package com.epam.reportportal.marketplace.config;

import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.user;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.multipart;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.webmvc.test.autoconfigure.AutoConfigureMockMvc;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest
@AutoConfigureMockMvc
class SecurityAuthorizationTest {

  @Autowired
  private MockMvc mockMvc;

  @Test
  void oidcPublisherCannotAccessOperatorOnlyEndpoints() throws Exception {
    mockMvc.perform(get("/api/v1/licenses")
            .with(user("github-actions").roles("OIDC_PUBLISH")))
        .andExpect(status().isForbidden());
  }

  @Test
  void oidcPublisherCanAccessFirstPublishEndpoint() throws Exception {
    mockMvc.perform(multipart("/api/v1/plugins")
            .with(user("github-actions").roles("OIDC_PUBLISH")))
        .andExpect(result ->
            assertNotEquals(403, result.getResponse().getStatus()));
  }

  @Test
  void oidcPublisherCanAccessVersionPublishEndpoint() throws Exception {
    mockMvc.perform(multipart("/api/v1/plugins/plugin-test/versions")
            .with(user("github-actions").roles("OIDC_PUBLISH")))
        .andExpect(result ->
            assertNotEquals(403, result.getResponse().getStatus()));
  }

  @Test
  void operatorCanAccessOperatorOnlyEndpoints() throws Exception {
    mockMvc.perform(get("/api/v1/licenses")
            .with(user("operator").roles("OPERATOR")))
        .andExpect(status().isOk());
  }
}
