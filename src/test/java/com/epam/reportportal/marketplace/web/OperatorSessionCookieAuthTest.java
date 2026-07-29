package com.epam.reportportal.marketplace.web;

import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.not;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.cookie;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.header;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.epam.reportportal.marketplace.auth.GitHubOAuthService;
import com.epam.reportportal.marketplace.auth.OperatorSessionCookie;
import com.epam.reportportal.marketplace.web.dto.AuthTokenResponseDto;
import org.junit.jupiter.api.Test;
import org.mockito.Mockito;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.webmvc.test.autoconfigure.AutoConfigureMockMvc;
import org.springframework.http.MediaType;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.MvcResult;

@SpringBootTest
@AutoConfigureMockMvc
class OperatorSessionCookieAuthTest {

  @Autowired
  private MockMvc mockMvc;

  @MockitoBean
  private GitHubOAuthService gitHubOAuthService;

  @Test
  void adminLoginSetsHttpOnlySessionCookieAndCookieAuthorizesLaterRequests() throws Exception {
    MvcResult login = mockMvc.perform(post("/api/v1/auth/login")
            .contentType(MediaType.APPLICATION_JSON)
            .content("{\"username\":\"admin\",\"password\":\"admin\"}"))
        .andExpect(status().isOk())
        .andExpect(cookie().exists(OperatorSessionCookie.NAME))
        .andExpect(cookie().httpOnly(OperatorSessionCookie.NAME, true))
        .andExpect(cookie().path(OperatorSessionCookie.NAME, "/"))
        .andExpect(jsonPath("$.accessToken").isNotEmpty())
        .andReturn();

    String cookie = login.getResponse().getCookie(OperatorSessionCookie.NAME).getValue();

    mockMvc.perform(get("/api/v1/licenses").cookie(
            new jakarta.servlet.http.Cookie(OperatorSessionCookie.NAME, cookie)))
        .andExpect(status().isOk());
  }

  @Test
  void oauthCallbackRedirectsWithoutPuttingTokenInTheLocation() throws Exception {
    Mockito.when(gitHubOAuthService.handleCallback(
            Mockito.eq("code"),
            Mockito.eq("state"),
            Mockito.any(),
            Mockito.any()))
        .thenReturn(new AuthTokenResponseDto("operator.jwt.token", "Bearer", 3600));

    mockMvc.perform(get("/api/v1/auth/github/callback")
            .param("code", "code")
            .param("state", "state"))
        .andExpect(status().isFound())
        .andExpect(header().string("Location", "/operator/"))
        .andExpect(header().string("Location", not(containsString("token="))))
        .andExpect(cookie().exists(OperatorSessionCookie.NAME))
        .andExpect(cookie().httpOnly(OperatorSessionCookie.NAME, true))
        .andExpect(cookie().value(OperatorSessionCookie.NAME, "operator.jwt.token"));
  }

  @Test
  void logoutClearsTheSessionCookie() throws Exception {
    MvcResult login = mockMvc.perform(post("/api/v1/auth/login")
            .contentType(MediaType.APPLICATION_JSON)
            .content("{\"username\":\"admin\",\"password\":\"admin\"}"))
        .andExpect(status().isOk())
        .andReturn();
    var session = login.getResponse().getCookie(OperatorSessionCookie.NAME);

    mockMvc.perform(post("/api/v1/auth/logout").cookie(session))
        .andExpect(status().isNoContent())
        .andExpect(cookie().maxAge(OperatorSessionCookie.NAME, 0));
  }
}
