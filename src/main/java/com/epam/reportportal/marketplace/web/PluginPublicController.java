package com.epam.reportportal.marketplace.web;

import com.epam.reportportal.marketplace.service.CatalogueService;
import com.epam.reportportal.marketplace.web.dto.PluginDetailDto;
import com.epam.reportportal.marketplace.web.dto.PluginListResponseDto;
import com.epam.reportportal.marketplace.web.dto.PluginVersionDetailDto;
import com.epam.reportportal.marketplace.web.dto.PluginVersionListResponseDto;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api/v1/plugins")
public class PluginPublicController {

  private final CatalogueService catalogueService;

  public PluginPublicController(CatalogueService catalogueService) {
    this.catalogueService = catalogueService;
  }

  @GetMapping
  PluginListResponseDto listPlugins(
      @RequestParam(required = false) String category,
      @RequestParam(required = false, name = "q") String query) {
    return catalogueService.listPlugins(category, query);
  }

  @GetMapping("/{pluginId}")
  PluginDetailDto getPlugin(@PathVariable String pluginId) {
    return catalogueService.getPlugin(pluginId);
  }

  @GetMapping("/{pluginId}/versions")
  PluginVersionListResponseDto listVersions(@PathVariable String pluginId) {
    return catalogueService.listVersions(pluginId);
  }

  @GetMapping("/{pluginId}/versions/{version}")
  PluginVersionDetailDto getVersionDetail(@PathVariable String pluginId, @PathVariable String version) {
    return catalogueService.getVersionDetail(pluginId, version);
  }
}
