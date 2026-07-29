package com.epam.reportportal.marketplace.support;

import java.util.Map;
import org.springframework.boot.resttestclient.TestRestTemplate;
import org.springframework.core.io.ByteArrayResource;
import org.springframework.http.HttpEntity;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpMethod;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.util.LinkedMultiValueMap;
import org.springframework.util.MultiValueMap;

/** Helpers for driving the operator publish endpoints over real HTTP in integration tests. */
public final class PublishRequests {

  private PublishRequests() {}

  public static <T> ResponseEntity<T> publishFirst(
      TestRestTemplate restTemplate, byte[] jar, Class<T> responseType) {
    HttpHeaders headers = new HttpHeaders();
    headers.setContentType(MediaType.MULTIPART_FORM_DATA);
    headers.setBearerAuth(operatorToken(restTemplate));

    MultiValueMap<String, Object> body = new LinkedMultiValueMap<>();
    body.add("jar", new ByteArrayResource(jar) {
      @Override
      public String getFilename() {
        return "plugin.jar";
      }
    });

    return restTemplate.exchange(
        "/api/v1/plugins", HttpMethod.POST, new HttpEntity<>(body, headers), responseType);
  }

  /** Logs in with the local-profile admin credentials and returns the session JWT. */
  public static String operatorToken(TestRestTemplate restTemplate) {
    Map<?, ?> login = restTemplate.postForObject(
        "/api/v1/auth/login", Map.of("username", "admin", "password", "admin"), Map.class);
    return (String) login.get("accessToken");
  }
}
