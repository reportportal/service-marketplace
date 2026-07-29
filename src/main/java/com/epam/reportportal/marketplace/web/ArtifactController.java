package com.epam.reportportal.marketplace.web;

import com.epam.reportportal.marketplace.service.ArtifactService;
import com.epam.reportportal.marketplace.web.dto.ArtifactResult;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api/v1/plugins/{pluginId}/versions/{version}")
public class ArtifactController {

  private final ArtifactService artifactService;

  public ArtifactController(ArtifactService artifactService) {
    this.artifactService = artifactService;
  }

  @GetMapping("/artifact")
  ResponseEntity<?> getArtifact(
      @PathVariable String pluginId,
      @PathVariable String version,
      @RequestHeader(value = "Authorization", required = false) String authorization) {
    String licenseJwt = extractBearer(authorization);
    ArtifactResult result = artifactService.resolveArtifact(pluginId, version, licenseJwt);
    if (result.type() == ArtifactResult.Type.REDIRECT) {
      return ResponseEntity.status(HttpStatus.FOUND)
          .header(HttpHeaders.LOCATION, result.redirectUrl())
          .build();
    }
    return ResponseEntity.ok(result.premium());
  }

  private String extractBearer(String authorization) {
    if (authorization != null && authorization.startsWith("Bearer ")) {
      return authorization.substring(7);
    }
    return null;
  }
}
