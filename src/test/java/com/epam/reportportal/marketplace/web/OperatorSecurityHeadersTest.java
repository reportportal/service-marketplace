package com.epam.reportportal.marketplace.web;

import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.not;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.header;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.webmvc.test.autoconfigure.AutoConfigureMockMvc;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.MvcResult;

@SpringBootTest
@AutoConfigureMockMvc
class OperatorSecurityHeadersTest {

  @Autowired
  private MockMvc mockMvc;

  @Test
  void operatorPageSendsStrictContentSecurityPolicy() throws Exception {
    mockMvc.perform(get("/operator/index.html"))
        .andExpect(status().isOk())
        .andExpect(header().string("Content-Security-Policy", containsString("script-src 'self'")))
        .andExpect(header().string("Content-Security-Policy", containsString("style-src 'self'")))
        .andExpect(header().string("Content-Security-Policy", containsString("object-src 'none'")))
        .andExpect(header().string("Content-Security-Policy", containsString("frame-ancestors 'none'")))
        .andExpect(header().string(
            "Content-Security-Policy", not(containsString("unsafe-inline"))));
  }

  @Test
  void operatorPageHasNoInlineScriptOrStyleBlocks() throws Exception {
    MvcResult result = mockMvc.perform(get("/operator/index.html"))
        .andExpect(status().isOk())
        .andReturn();
    String html = result.getResponse().getContentAsString();
    org.junit.jupiter.api.Assertions.assertFalse(
        html.toLowerCase().contains("<script>"), "inline script block must be absent");
    org.junit.jupiter.api.Assertions.assertFalse(
        html.toLowerCase().contains("<style"), "inline style block must be absent");
    org.junit.jupiter.api.Assertions.assertTrue(html.contains("/operator/operator.js"), html);
    org.junit.jupiter.api.Assertions.assertTrue(html.contains("/operator/operator.css"), html);
  }
}
