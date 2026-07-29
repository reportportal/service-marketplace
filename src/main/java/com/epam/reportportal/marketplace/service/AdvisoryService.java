package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.domain.AdvisoryJson;
import com.epam.reportportal.marketplace.domain.AdvisorySeverity;
import com.epam.reportportal.marketplace.domain.PluginJson;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.epam.reportportal.marketplace.util.JsonStore;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.epam.reportportal.marketplace.web.dto.SecurityAdvisoryDto;
import com.epam.reportportal.marketplace.web.error.NotFoundException;
import tools.jackson.databind.ObjectMapper;
import java.time.Instant;
import java.util.List;
import org.springframework.stereotype.Service;

@Service
public class AdvisoryService {

  private final ObjectStore objectStore;
  private final ObjectMapper objectMapper;
  private final CdnInvalidationService cdnInvalidationService;

  public AdvisoryService(
      ObjectStore objectStore,
      ObjectMapper objectMapper,
      CdnInvalidationService cdnInvalidationService) {
    this.objectStore = objectStore;
    this.objectMapper = objectMapper;
    this.cdnInvalidationService = cdnInvalidationService;
  }

  public SecurityAdvisoryDto attachAdvisory(String pluginId, String version, AdvisorySeverity severity, String text) {
    PluginJson plugin = JsonStore.read(objectStore, objectMapper, StoragePaths.pluginJson(pluginId), PluginJson.class);
    if (plugin == null || plugin.isRemoved() || !plugin.getVersions().contains(version)) {
      throw new NotFoundException("Version not found: " + version);
    }
    Instant attachedAt = Instant.now();
    AdvisoryJson advisory = new AdvisoryJson(severity, text, attachedAt);
    JsonStore.write(objectStore, objectMapper, StoragePaths.advisoryPath(pluginId, version), advisory);
    cdnInvalidationService.invalidatePaths(List.of(
        "/plugins/" + pluginId + "/versions/" + version + "/advisory.json"));
    return new SecurityAdvisoryDto(severity, text, attachedAt);
  }
}
