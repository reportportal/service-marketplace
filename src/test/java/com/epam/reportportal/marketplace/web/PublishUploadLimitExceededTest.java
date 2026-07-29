package com.epam.reportportal.marketplace.web;

import static org.junit.jupiter.api.Assertions.assertEquals;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.support.PublishRequests;
import com.epam.reportportal.marketplace.support.TestJarFactory;
import com.epam.reportportal.marketplace.web.dto.ErrorResponseDto;
import java.nio.file.Path;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.test.web.client.TestRestTemplate;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

/**
 * A bundle over the configured limit must report the size problem instead of a generic 500.
 * The limit is lowered here so the test does not have to move a 128MB body.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = {
        "spring.servlet.multipart.max-file-size=64KB",
        "spring.servlet.multipart.max-request-size=64KB"
    })
class PublishUploadLimitExceededTest {

  @TempDir
  static Path storageRoot;

  @Autowired
  private TestRestTemplate restTemplate;

  @DynamicPropertySource
  static void storageProperties(DynamicPropertyRegistry registry) {
    registry.add("marketplace.storage.local.root", () -> storageRoot.toString());
  }

  @Test
  void rejectsOversizedBundleWithPayloadTooLarge() throws Exception {
    byte[] jar = TestJarFactory.createJar(
        TestJarFactory.sampleManifest("plugin-oversized", "1.0.0", AccessTier.PUBLIC), 256 * 1024);

    ResponseEntity<ErrorResponseDto> response = PublishRequests.publishFirst(
        restTemplate, jar, ErrorResponseDto.class);

    assertEquals(HttpStatus.PAYLOAD_TOO_LARGE, response.getStatusCode());
    assertEquals("PAYLOAD_TOO_LARGE", response.getBody().code());
  }
}
