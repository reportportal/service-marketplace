package com.epam.reportportal.marketplace.web;

import com.epam.reportportal.marketplace.auth.OidcPublishAuthService;
import com.epam.reportportal.marketplace.domain.BlockedVersion;
import com.epam.reportportal.marketplace.service.LifecycleService;
import com.epam.reportportal.marketplace.service.ManifestExtractor;
import com.epam.reportportal.marketplace.service.PublishService;
import com.epam.reportportal.marketplace.web.dto.BlockVersionRequestDto;
import com.epam.reportportal.marketplace.web.dto.PluginOperatorStateDto;
import com.epam.reportportal.marketplace.web.dto.PluginTombstoneDto;
import com.epam.reportportal.marketplace.web.dto.PublishBundle;
import com.epam.reportportal.marketplace.web.dto.PublishResponseDto;
import com.epam.reportportal.marketplace.web.dto.RemovePluginRequestDto;
import com.epam.reportportal.marketplace.web.dto.SetTierRequestDto;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.validation.Valid;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.security.core.Authentication;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.PatchMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestPart;
import org.springframework.web.bind.annotation.ResponseStatus;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.multipart.MultipartFile;

@RestController
@RequestMapping("/api/v1/plugins")
public class PluginOperatorController {

  private final PublishService publishService;
  private final LifecycleService lifecycleService;
  private final OidcPublishAuthService oidcPublishAuthService;
  private final ManifestExtractor manifestExtractor;

  public PluginOperatorController(
      PublishService publishService,
      LifecycleService lifecycleService,
      OidcPublishAuthService oidcPublishAuthService,
      ManifestExtractor manifestExtractor) {
    this.publishService = publishService;
    this.lifecycleService = lifecycleService;
    this.oidcPublishAuthService = oidcPublishAuthService;
    this.manifestExtractor = manifestExtractor;
  }

  @PostMapping(consumes = MediaType.MULTIPART_FORM_DATA_VALUE)
  @ResponseStatus(HttpStatus.CREATED)
  PublishResponseDto publishFirst(
      @RequestPart("jar") MultipartFile jar,
      @RequestPart(value = "changelog", required = false) MultipartFile changelog,
      @RequestPart(value = "screenshots", required = false) List<MultipartFile> screenshots,
      HttpServletRequest request) throws IOException {
    PublishBundle bundle = toBundle(jar, changelog, screenshots);
    maybeValidateOidc(request, manifestExtractor.extract(bundle.jar()).id());
    return publishService.publishFirst(bundle);
  }

  @PostMapping(value = "/{pluginId}/versions", consumes = MediaType.MULTIPART_FORM_DATA_VALUE)
  @ResponseStatus(HttpStatus.CREATED)
  PublishResponseDto publishVersion(
      @PathVariable String pluginId,
      @RequestPart("jar") MultipartFile jar,
      @RequestPart(value = "changelog", required = false) MultipartFile changelog,
      @RequestPart(value = "screenshots", required = false) List<MultipartFile> screenshots,
      HttpServletRequest request) throws IOException {
    PublishBundle bundle = toBundle(jar, changelog, screenshots);
    maybeValidateOidc(request, pluginId);
    return publishService.publishVersion(pluginId, bundle);
  }

  @PatchMapping("/{pluginId}")
  PluginOperatorStateDto patchTier(@PathVariable String pluginId, @Valid @RequestBody SetTierRequestDto request) {
    return lifecycleService.patchTier(pluginId, request.tier());
  }

  @DeleteMapping("/{pluginId}")
  PluginTombstoneDto removePlugin(@PathVariable String pluginId, @Valid @RequestBody RemovePluginRequestDto request) {
    return lifecycleService.removePlugin(pluginId, request.removalReason(), currentSubject());
  }

  @PostMapping("/{pluginId}/versions/{version}/block")
  BlockedVersion blockVersion(
      @PathVariable String pluginId,
      @PathVariable String version,
      @Valid @RequestBody BlockVersionRequestDto request) {
    return lifecycleService.blockVersion(pluginId, version, request.reason());
  }

  private PublishBundle toBundle(
      MultipartFile jar, MultipartFile changelog, List<MultipartFile> screenshots) throws IOException {
    List<PublishBundle.ScreenshotPart> screenshotParts = new ArrayList<>();
    if (screenshots != null) {
      for (MultipartFile file : screenshots) {
        String name = file.getOriginalFilename() != null ? file.getOriginalFilename() : "screenshot.png";
        name = name.replace('\\', '/');
        if (name.contains("/")) {
          name = name.substring(name.lastIndexOf('/') + 1);
        }
        screenshotParts.add(new PublishBundle.ScreenshotPart(name, file.getBytes()));
      }
    }
    return new PublishBundle(
        jar.getBytes(),
        changelog != null ? changelog.getBytes() : null,
        screenshotParts);
  }

  private void maybeValidateOidc(HttpServletRequest request, String pluginId) {
    String auth = request.getHeader("Authorization");
    if (auth != null && auth.startsWith("Bearer ")) {
      String token = auth.substring(7);
      if (oidcPublishAuthService.isOidcToken(token)) {
        oidcPublishAuthService.validatePublishToken(token, pluginId);
      }
    }
  }

  private String currentSubject() {
    Authentication auth = SecurityContextHolder.getContext().getAuthentication();
    return auth != null ? auth.getName() : "operator";
  }
}
