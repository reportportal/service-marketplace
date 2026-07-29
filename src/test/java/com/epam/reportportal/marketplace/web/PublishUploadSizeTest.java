package com.epam.reportportal.marketplace.web;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.support.PublishRequests;
import com.epam.reportportal.marketplace.support.TestJarFactory;
import com.epam.reportportal.marketplace.web.dto.PublishResponseDto;
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
 * Guards against the Boot default multipart limits (1MB per file) silently rejecting every
 * realistic publish bundle.
 */
@SpringBootTest(webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT)
class PublishUploadSizeTest {

  private static final int FOUR_MB = 4 * 1024 * 1024;

  @TempDir
  static Path storageRoot;

  @Autowired
  private TestRestTemplate restTemplate;

  @DynamicPropertySource
  static void storageProperties(DynamicPropertyRegistry registry) {
    registry.add("marketplace.storage.local.root", () -> storageRoot.toString());
  }

  @Test
  void acceptsBundleLargerThanTheDefaultMultipartLimit() throws Exception {
    byte[] jar = TestJarFactory.createJar(
        TestJarFactory.sampleManifest("plugin-large", "1.0.0", AccessTier.PUBLIC), FOUR_MB);
    assertTrue(jar.length > 1024 * 1024, "test jar must exceed the 1MB Boot default");

    ResponseEntity<PublishResponseDto> response = PublishRequests.publishFirst(
        restTemplate, jar, PublishResponseDto.class);

    assertEquals(HttpStatus.CREATED, response.getStatusCode());
    assertEquals("plugin-large", response.getBody().pluginId());
    assertEquals("1.0.0", response.getBody().version());
  }
}
