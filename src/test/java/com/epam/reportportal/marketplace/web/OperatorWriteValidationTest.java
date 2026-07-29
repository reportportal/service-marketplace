package com.epam.reportportal.marketplace.web;

import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.user;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.delete;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.patch;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.webmvc.test.autoconfigure.AutoConfigureMockMvc;
import org.springframework.http.MediaType;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest
@AutoConfigureMockMvc
class OperatorWriteValidationTest {

  @Autowired
  private MockMvc mockMvc;

  @Test
  void rejectsBlankAdminLoginFields() throws Exception {
    mockMvc.perform(post("/api/v1/auth/login")
            .contentType(MediaType.APPLICATION_JSON)
            .content("{\"username\":\"\",\"password\":\"\"}"))
        .andExpect(status().isUnprocessableContent())
        .andExpect(jsonPath("$.code").value("VALIDATION_ERROR"));
  }

  @Test
  void rejectsMissingBlockReason() throws Exception {
    mockMvc.perform(post("/api/v1/plugins/plugin-test/versions/1.0.0/block")
            .with(user("operator").roles("OPERATOR"))
            .contentType(MediaType.APPLICATION_JSON)
            .content("{}"))
        .andExpect(status().isUnprocessableContent())
        .andExpect(jsonPath("$.errors[0].field").value("reason"));
  }

  @Test
  void rejectsOversizedRemovalReason() throws Exception {
    String reason = "x".repeat(2001);
    mockMvc.perform(delete("/api/v1/plugins/plugin-test")
            .with(user("operator").roles("OPERATOR"))
            .contentType(MediaType.APPLICATION_JSON)
            .content("{\"removalReason\":\"" + reason + "\"}"))
        .andExpect(status().isUnprocessableContent())
        .andExpect(jsonPath("$.errors[0].field").value("removalReason"));
  }

  @Test
  void rejectsBlankAdvisoryText() throws Exception {
    mockMvc.perform(post("/api/v1/plugins/plugin-test/versions/1.0.0/advisory")
            .with(user("operator").roles("OPERATOR"))
            .contentType(MediaType.APPLICATION_JSON)
            .content("{\"severity\":\"high\",\"text\":\"\"}"))
        .andExpect(status().isUnprocessableContent())
        .andExpect(jsonPath("$.errors[?(@.field=='text')]").exists());
  }

  @Test
  void rejectsOversizedAdvisoryTextViaBeanValidation() {
    var validator = jakarta.validation.Validation.buildDefaultValidatorFactory().getValidator();
    var dto = new com.epam.reportportal.marketplace.web.dto.AttachAdvisoryRequestDto(
        com.epam.reportportal.marketplace.domain.AdvisorySeverity.HIGH, "x".repeat(5001));
    var violations = validator.validate(dto);
    org.junit.jupiter.api.Assertions.assertTrue(
        violations.stream().anyMatch(v -> "text".equals(v.getPropertyPath().toString())));
  }

  @Test
  void rejectsMissingTier() throws Exception {
    mockMvc.perform(patch("/api/v1/plugins/plugin-test")
            .with(user("operator").roles("OPERATOR"))
            .contentType(MediaType.APPLICATION_JSON)
            .content("{}"))
        .andExpect(status().isUnprocessableContent())
        .andExpect(jsonPath("$.errors[0].field").value("tier"));
  }

  @Test
  void rejectsInvalidCustomerIdOnCreate() throws Exception {
    mockMvc.perform(post("/api/v1/licenses")
            .with(user("operator").roles("OPERATOR"))
            .contentType(MediaType.APPLICATION_JSON)
            .content("{\"customerId\":\"ACME/Corp\"}"))
        .andExpect(status().isUnprocessableContent())
        .andExpect(jsonPath("$.errors[?(@.field=='customerId')]").exists());
  }

  @Test
  void rejectsInvalidCustomerIdOnRevoke() throws Exception {
    mockMvc.perform(delete("/api/v1/licenses/Bad_Id")
            .with(user("operator").roles("OPERATOR")))
        .andExpect(status().isUnprocessableContent())
        .andExpect(jsonPath("$.errors[0].field").value("customerId"));
  }

  @Test
  void acceptsBoundaryLengthCustomerId() throws Exception {
    // 64-char slug: unique per run so repeated suite executions do not collide.
    String customerId = ("t" + Long.toHexString(System.nanoTime()) + "x".repeat(64))
        .substring(0, 64)
        .toLowerCase();
    mockMvc.perform(post("/api/v1/licenses")
            .with(user("operator").roles("OPERATOR"))
            .contentType(MediaType.APPLICATION_JSON)
            .content("{\"customerId\":\"" + customerId + "\"}"))
        .andExpect(status().isCreated())
        .andExpect(jsonPath("$.customerId").value(customerId));
  }
}
