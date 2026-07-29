package com.epam.reportportal.marketplace.web;

import com.epam.reportportal.marketplace.storage.ObjectStore;
import jakarta.servlet.http.HttpServletRequest;
import java.nio.charset.StandardCharsets;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.http.HttpHeaders;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

/**
 * Serves registry objects under {@code /cdn/**} for the local storage profile,
 * standing in for Cloud CDN + GCS backend bucket during development.
 */
@RestController
@ConditionalOnProperty(prefix = "marketplace.storage", name = "type", havingValue = "local", matchIfMissing = true)
public class LocalCdnController {

  private final ObjectStore objectStore;

  public LocalCdnController(ObjectStore objectStore) {
    this.objectStore = objectStore;
  }

  @GetMapping("/cdn/**")
  public ResponseEntity<byte[]> get(HttpServletRequest request) {
    String uri = request.getRequestURI();
    String prefix = request.getContextPath() + "/cdn/";
    if (!uri.startsWith(prefix)) {
      return ResponseEntity.notFound().build();
    }
    String objectPath = uri.substring(prefix.length());
    if (objectPath.isBlank() || objectPath.contains("..") || !objectStore.exists(objectPath)) {
      return ResponseEntity.notFound().build();
    }
    byte[] bytes = objectStore.readBytes(objectPath);
    return ResponseEntity.ok()
        .header(HttpHeaders.CACHE_CONTROL, "public, max-age=3600")
        .contentType(contentType(objectPath))
        .body(bytes);
  }

  private static MediaType contentType(String path) {
    String lower = path.toLowerCase();
    if (lower.endsWith(".jar")) {
      return MediaType.parseMediaType("application/java-archive");
    }
    if (lower.endsWith(".json")) {
      return MediaType.APPLICATION_JSON;
    }
    if (lower.endsWith(".md")) {
      return new MediaType("text", "markdown", StandardCharsets.UTF_8);
    }
    if (lower.endsWith(".png")) {
      return MediaType.IMAGE_PNG;
    }
    if (lower.endsWith(".jpg") || lower.endsWith(".jpeg")) {
      return MediaType.IMAGE_JPEG;
    }
    return MediaType.APPLICATION_OCTET_STREAM;
  }
}
