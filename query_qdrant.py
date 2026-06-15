#!/usr/bin/env python3
"""
query_qdrant.py

Query the Qdrant vector database using a text query.
It embeds the query using the same embedding server and retrieves the most relevant chunks.

Usage:
  python query_qdrant.py "how do I configure the server?"
"""

import os
import sys
import argparse
import requests
from dotenv import load_dotenv
from qdrant_client import QdrantClient

# Load .env configurations
load_dotenv()

def main():
    parser = argparse.ArgumentParser(description="Query the Qdrant knowledge base.")
    parser.add_argument("query", help="The text query to search for.")
    parser.add_argument(
        "--qdrant-url",
        default=os.environ.get("QDRANT_URL", "http://localhost:6333"),
        help="Qdrant API URL.",
    )
    parser.add_argument(
        "--qdrant-api-key",
        default=os.environ.get("QDRANT_API_KEY", None),
        help="Qdrant API Key (optional).",
    )
    parser.add_argument(
        "--collection",
        default=os.environ.get("QDRANT_COLLECTION", "mdchunk"),
        help="Qdrant collection to query.",
    )
    parser.add_argument(
        "--embed-url",
        default=os.environ.get("QDRANT_EMBED_URL", os.environ.get("SURREAL_EMBED_URL", "http://127.0.0.1:8008/v1")),
        help="Base URL of the OpenAI-compatible embedding API.",
    )
    parser.add_argument(
        "--embed-model",
        default=os.environ.get("QDRANT_EMBED_MODEL", os.environ.get("SURREAL_EMBED_MODEL", "bge-m3")),
        help="Embedding model name.",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=5,
        help="Number of results to return.",
    )

    args = parser.parse_args()

    # 1. Embed the query text
    print(f"Embedding query: '{args.query}' using model '{args.embed_model}'...")
    try:
        resp = requests.post(
            f"{args.embed_url}/embeddings",
            json={"model": args.embed_model, "input": [args.query]},
            timeout=30,
        )
        resp.raise_for_status()
        query_vector = resp.json()["data"][0]["embedding"]
        print(f"Successfully generated query embedding (dim={len(query_vector)}).")
    except Exception as e:
        print(f"Failed to generate query embedding: {e}", file=sys.stderr)
        sys.exit(1)

    # 2. Search Qdrant
    print(f"Searching Qdrant collection '{args.collection}' at {args.qdrant_url}...")
    try:
        client = QdrantClient(url=args.qdrant_url, api_key=args.qdrant_api_key)
        results = client.search(
            collection_name=args.collection,
            query_vector=query_vector,
            limit=args.limit,
        )
        
        if not results:
            print("No matching results found.")
            return

        print(f"\nFound {len(results)} matches:\n" + "="*80)
        for i, res in enumerate(results):
            payload = res.payload or {}
            score = res.score
            fp = payload.get("file_path", "unknown")
            content = payload.get("content", "").strip()
            
            print(f"Result #{i+1} | Score: {score:.4f} | Source: {fp}")
            print("-" * 80)
            print(content)
            print("=" * 80)

    except Exception as e:
        print(f"Failed to query Qdrant: {e}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
