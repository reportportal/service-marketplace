package com.epam.reportportal.marketplace.web;

import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.epam.reportportal.marketplace.util.StoragePaths;
import jakarta.servlet.http.HttpServletRequest;
import java.nio.charset.StandardCharsets;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.http.HttpHeaders;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestParam;
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
  public ResponseEntity<byte[]> getPublic(HttpServletRequest request) {
    String objectPath = objectPath(request, "/cdn/");
    if (objectPath == null
        || objectPath.isBlank()
        || objectPath.contains("..")
        || !StoragePaths.isPublic(objectPath)
        || !objectStore.exists(objectPath)) {
      return ResponseEntity.notFound().build();
    }
    return serve(objectPath, "public, max-age=3600");
  }

  @GetMapping("/cdn-private/**")
  public ResponseEntity<byte[]> getSigned(
      HttpServletRequest request,
      @RequestParam long expires,
      @RequestParam String signature) {
    String objectPath = objectPath(request, "/cdn-private/");
    if (objectPath == null
        || objectPath.isBlank()
        || objectPath.contains("..")
        || !StoragePaths.isPrivate(objectPath)
        || !objectStore.exists(objectPath)
        || !objectStore.verifySignedUrl(objectPath, expires, signature)) {
      return ResponseEntity.notFound().build();
    }
    return serve(objectPath, "private, no-store");
  }

  private ResponseEntity<byte[]> serve(String objectPath, String cacheControl) {
    byte[] bytes = objectStore.readBytes(objectPath);
    return ResponseEntity.ok()
        .header(HttpHeaders.CACHE_CONTROL, cacheControl)
        .contentType(contentType(objectPath))
        .body(bytes);
  }

  private static String objectPath(HttpServletRequest request, String routePrefix) {
    String uri = request.getRequestURI();
    String prefix = request.getContextPath() + routePrefix;
    return uri.startsWith(prefix) ? uri.substring(prefix.length()) : null;
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
